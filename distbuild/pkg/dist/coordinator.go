//go:build !solution

package dist

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"gitlab.com/slon/shad-go/distbuild/pkg/api"
	"gitlab.com/slon/shad-go/distbuild/pkg/build"
	"gitlab.com/slon/shad-go/distbuild/pkg/filecache"
	"gitlab.com/slon/shad-go/distbuild/pkg/scheduler"
)

type buildData struct {
	mu sync.Mutex

	jobs         []build.Job
	stWriter     api.StatusWriter
	jobsDoneCnt  int
	fileIDByName map[string]build.ID
	buildID      build.ID
}

type Coordinator struct {
	log       *zap.Logger
	files     *filecache.Cache
	scheduler *scheduler.Scheduler

	builds     sync.Map
	buildByJob sync.Map

	mux *http.ServeMux
}

var defaultConfig = scheduler.Config{
	CacheTimeout: time.Millisecond * 10,
	DepsTimeout:  time.Millisecond * 100,
}

func processFinishedJob(c *Coordinator, jobRes *api.JobResult, workerID *api.WorkerID) {
	c.log.Debug("coordinator heartbeat received job finished", zap.String("jbp_id", jobRes.ID.String()))
	c.scheduler.OnJobComplete(*workerID, jobRes.ID, jobRes)

	buildCh, ok := c.buildByJob.Load(jobRes.ID)
	if !ok { // invariant
		panic("heartbeat for undefined job")
	}

	data := <-buildCh.(chan *buildData)
	c.log.Debug(fmt.Sprintf("found buildID %v for jobID %v", data.buildID, jobRes.ID))

	data.mu.Lock()
	defer data.mu.Unlock()

	if data.stWriter == nil { // invariant
		panic("data.StWriter is nil")
	}

	sendStatus := func(upd *api.StatusUpdate) {
		if err := data.stWriter.Updated(upd); err != nil {
			// TODO: maybe send signal to finish building process with error for this build_id
			c.log.Error("error during sending status of job", zap.Any("status", *upd), zap.String("job_id", jobRes.ID.String()), zap.Error(err))
		}
	}

	upd := &api.StatusUpdate{JobFinished: jobRes}
	sendStatus(upd)

	data.jobsDoneCnt++
	totalJobs := len(data.jobs)

	c.log.Debug(fmt.Sprintf("job %v done, %v of %v jobs done for build %v", jobRes.ID, data.jobsDoneCnt, totalJobs, data.buildID))

	if data.jobsDoneCnt == totalJobs {
		c.log.Debug(fmt.Sprintf("all jobs done for buildID %v", data.buildID))

		upd := &api.StatusUpdate{BuildFinished: &api.BuildFinished{}}
		sendStatus(upd)
	}
}

func (c *Coordinator) Heartbeat(ctx context.Context, req *api.HeartbeatRequest) (*api.HeartbeatResponse, error) {
	for _, finished := range req.FinishedJob {
		processFinishedJob(c, &finished, &req.WorkerID)
	}
	var resp api.HeartbeatResponse
	resp.JobsToRun = make(map[build.ID]api.JobSpec)

	for i := 0; i < req.FreeSlots; i++ {
		job := c.scheduler.PickJob(ctx, req.WorkerID)
		if job == nil {
			c.log.Debug("PickJob returned nil")
			break
		}
		if wID, ok := c.scheduler.LocateArtifact(job.Job.ID); ok {
			c.log.Info(fmt.Sprintf("skip job %v because it's artiffact is already in cache", job.Job.ID))
			processFinishedJob(c, &api.JobResult{ID: job.Job.ID}, &wID)
			continue
		}
		resp.JobsToRun[job.Job.ID] = *job.Job
	}

	return &resp, nil
}

func (c *Coordinator) StartBuild(ctx context.Context, req *api.BuildRequest, w api.StatusWriter) error {
	c.log.Debug("service StartBuild starts", zap.Any("req", *req))
	id := build.NewID()

	fileIDByName := make(map[string]build.ID)

	needFiles := make([]build.ID, 0, len(req.Graph.SourceFiles))
	for fileID, fileName := range req.Graph.SourceFiles {
		fileIDByName[fileName] = fileID
		if _, unlock, err := c.files.Get(fileID); err != nil { // check if file is in cache
			needFiles = append(needFiles, fileID)
		} else {
			unlock()
		}
	}

	data := buildData{jobs: build.TopSort(req.Graph.Jobs), stWriter: w, fileIDByName: fileIDByName, buildID: id}
	data.mu.Lock()
	defer data.mu.Unlock()

	for _, j := range req.Graph.Jobs {
		buildCh, _ := c.buildByJob.LoadOrStore(j.ID, make(chan *buildData, 10))
		buildCh.(chan *buildData) <- &data
	}

	c.builds.Store(id, &data)
	if data.stWriter == nil { // invariant
		panic("data.StWriter is nil")
	}
	if err := data.stWriter.Started(&api.BuildStarted{ID: id, MissingFiles: needFiles}); err != nil {
		return fmt.Errorf("couldn't send started status of build %v: %w", id, err)
	}

	return nil
}

func (c *Coordinator) SignalBuild(ctx context.Context, buildID build.ID, req *api.SignalRequest) (*api.SignalResponse, error) {
	c.log.Debug("service SignalBuild starts", zap.String("build_id", buildID.String()), zap.Any("req", *req))
	data_, ok := c.builds.Load(buildID)
	if !ok { // invariant
		panic(fmt.Sprintf("no data for buildID: %v", buildID))
	}
	data := data_.(*buildData)

	data.mu.Lock()
	defer data.mu.Unlock()

	if req.UploadDone != nil {
		for _, job := range data.jobs {

			sourceFiles := make(map[build.ID]string)

			for _, sf := range job.Inputs {
				sourceFiles[data.fileIDByName[sf]] = sf
			}

			arts := make(map[build.ID]api.WorkerID, len(job.Deps))

			for _, dep := range job.Deps {
				for {
					if wID, ok := c.scheduler.LocateArtifact(dep); ok {
						arts[dep] = wID
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			c.scheduler.ScheduleJob(&api.JobSpec{Job: job, SourceFiles: sourceFiles, Artifacts: arts})
		}
	}

	return &api.SignalResponse{}, nil
}

func NewCoordinator(
	log *zap.Logger,
	fileCache *filecache.Cache,
) *Coordinator {
	c := Coordinator{
		log:       log,
		files:     fileCache,
		scheduler: scheduler.NewScheduler(log, defaultConfig),
		mux:       http.NewServeMux(),
	}

	api.NewHeartbeatHandler(log, &c).Register(c.mux)
	api.NewBuildService(log, &c).Register(c.mux)
	filecache.NewHandler(log, fileCache).Register(c.mux)

	return &c
}

func (c *Coordinator) Stop() {
	c.scheduler.Stop()
}

func (c *Coordinator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mux.ServeHTTP(w, r)
}
