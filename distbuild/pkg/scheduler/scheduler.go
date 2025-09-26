//go:build !solution

package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"gitlab.com/slon/shad-go/distbuild/pkg/api"
	"gitlab.com/slon/shad-go/distbuild/pkg/build"
)

var TimeAfter = time.After

type PendingJob struct {
	Job      *api.JobSpec
	Finished chan struct{}
	Result   *api.JobResult
}

type Config struct {
	CacheTimeout time.Duration
	DepsTimeout  time.Duration
}

type Scheduler struct {
	l      *zap.Logger
	config Config

	jobsQue           chan *PendingJob
	artifactLocations sync.Map

	stopped   bool
	stoppedMu sync.RWMutex
	stoppedCh chan struct{}
}

func NewScheduler(l *zap.Logger, config Config) *Scheduler {
	return &Scheduler{
		l,
		config,

		make(chan *PendingJob, 100), // if chan is full (contains 100 elements) ScheduleJob will hang until next PickJob
		sync.Map{},

		false,
		sync.RWMutex{},
		make(chan struct{}),
	}
}

func (c *Scheduler) LocateArtifact(id build.ID) (api.WorkerID, bool) {
	workerID, ok := c.artifactLocations.Load(id.String())
	c.l.Debug(fmt.Sprintf("check if artifact %v is in cache: %v", id, ok))
	if ok {
		return workerID.(api.WorkerID), ok
	}
	return api.WorkerID(""), false
}

func (c *Scheduler) OnJobComplete(workerID api.WorkerID, jobID build.ID, res *api.JobResult) bool {
	c.l.Info(fmt.Sprintf("Job %v completed, res: %v", res.ID, *res))
	if res.ExitCode == 0 {
		c.artifactLocations.Store(res.ID.String(), workerID)
		return true
	}
	return false
}

func (c *Scheduler) ScheduleJob(job *api.JobSpec) *PendingJob {
	c.stoppedMu.RLock()
	c.l.Info("schedule job", zap.Any("job", *job))
	if c.stopped {
		return nil
	}
	c.stoppedMu.RUnlock()

	p := PendingJob{job, make(chan struct{}), &api.JobResult{ID: job.ID}}
	c.jobsQue <- &p

	return &p
}

func (c *Scheduler) PickJob(ctx context.Context, workerID api.WorkerID) *PendingJob {
	select {
	case job := <-c.jobsQue:
		c.l.Info("PickJob", zap.Any("jobSpec", *job.Job))
		return job
	case <-ctx.Done():
		c.l.Info("PickJob cancelled")
		return nil
	case <-c.stoppedCh:
		c.l.Info("PickJob: scheduler stopped")
		return nil
	}
}

func (c *Scheduler) Stop() {
	c.stoppedMu.Lock()
	c.stopped = true
	close(c.stoppedCh)
	c.stoppedMu.Unlock()
}
