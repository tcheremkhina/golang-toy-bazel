//go:build !solution

package worker

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"go.uber.org/zap"

	"gitlab.com/slon/shad-go/distbuild/pkg/api"
	"gitlab.com/slon/shad-go/distbuild/pkg/artifact"
	"gitlab.com/slon/shad-go/distbuild/pkg/build"
	"gitlab.com/slon/shad-go/distbuild/pkg/filecache"
)

type Worker struct {
	workerID            api.WorkerID
	coordinatorEndpoint string
	log                 *zap.SugaredLogger

	files     *filecache.Cache
	artifacts *artifact.Cache

	mux    *http.ServeMux
	client *api.HeartbeatClient

	filesClient *filecache.Client
}

func New(
	workerID api.WorkerID,
	coordinatorEndpoint string,
	log *zap.Logger,
	fileCache *filecache.Cache,
	artifacts *artifact.Cache,
) *Worker {
	mux := http.NewServeMux()
	filecache.NewHandler(log, fileCache).Register(mux)
	artifact.NewHandler(log, artifacts).Register(mux)
	return &Worker{
		workerID,
		coordinatorEndpoint,
		log.Sugar(),

		fileCache,
		artifacts,

		mux,
		api.NewHeartbeatClient(log, coordinatorEndpoint),

		filecache.NewClient(log, coordinatorEndpoint),
	}
}

func (w *Worker) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w.mux.ServeHTTP(rw, r)
}

func (w *Worker) Run(ctx context.Context) error {
	runningJobs := make([]build.ID, 0)
	finishedJobs := make([]api.JobResult, 0)
	addedArtifacts := make([]build.ID, 0)

	w.log.Debugf("start worker %v", w.workerID)

	for {
		hbReq := api.HeartbeatRequest{
			WorkerID:    w.workerID,
			RunningJobs: runningJobs,
			FreeSlots:   1,
			// for now algorithm works only for one slot on every worker
			// improve scheduling algorithm before change number of clots
			FinishedJob:    finishedJobs,
			AddedArtifacts: addedArtifacts,
		}

		resp, err := w.client.Heartbeat(ctx, &hbReq)
		if err != nil {
			err = fmt.Errorf("error diring heartbeat iteration: %w", err)
			return err
		}

		finishedJobs = nil
		addedArtifacts = nil

		w.log.Infof("%v received %v jobs to run", w.workerID, len(resp.JobsToRun))
		for _, spec := range resp.JobsToRun {

			w.log.Debugf("start to collect artifacts for job %v on worker %v", spec.ID, w.workerID)

			depsMap := make(map[build.ID]string)

			for artID, workID := range spec.Artifacts {
				path, unlock, err := w.artifacts.Get(artID)
				if err != nil { // artifact is not already in local cache
					err = artifact.Download(ctx, workID.String(), w.artifacts, artID)
					if err != nil {
						w.log.Errorf("error during downloading artifact %v for job %v : %v", artID, spec.ID, err)
						return err
					}
					path, unlock, err = w.artifacts.Get(artID)
					if err != nil { // invariant
						panic("error in get of just downloaded artifact")
					}
				}
				depsMap[artID] = path

				defer unlock()
				addedArtifacts = append(addedArtifacts, artID)
			}
			w.log.Debugf("artifacts for job %v collected, downloaded %v artifacts", spec.ID, len(spec.Artifacts))

			w.log.Debugf("start to collect source files for job %v on worker %v", spec.ID, w.workerID)
			for fileID := range spec.SourceFiles {
				w.log.Debugf("downloading file %v on worker %v", fileID, w.workerID)
				err := w.filesClient.Download(ctx, w.files, fileID)
				if err != nil {
					err = fmt.Errorf("error during downloading file %v: %w", fileID, err)
					w.log.Error(err.Error())
					return err
				}
			}
			w.log.Debugf("source files for job %v collected, downloaded %v files", spec.ID, len(spec.SourceFiles))

			w.log.Infof("creating artifact for job %v", spec.ID)
			path, createArtifactCommit, createArtifactAbort, err := w.artifacts.Create(spec.ID)
			if err != nil {
				w.log.Errorf("error during creating artifact %v: %v", spec.ID, err)
				return err
			}

			var bytesOut, bytesErr bytes.Buffer

			sourceDir, err := os.MkdirTemp("", "")
			if err != nil {
				panic(fmt.Sprintf("couldn't create temp dir for execute job spec %v", spec.ID))
			}

			unlockFiles := make([]func(), 0, len(spec.SourceFiles))

			unlockFilesFunc := func() {
				for i := len(unlockFiles) - 1; i >= 0; i-- {
					unlockFiles[i]()
				}
			}

			defer unlockFilesFunc()
			defer os.Remove(sourceDir)

			runAbort := func() {
				abortErr := createArtifactAbort()
				if abortErr != nil {
					w.log.Error("couldn't abort creating artifact", zap.Error(abortErr))
				}
			}

			for sfID, sfName := range spec.SourceFiles {
				path, unlock, err := w.files.Get(sfID)
				if err != nil {
					w.log.Errorf("error during copying file %v for job spec %v", sfName, spec.ID)
					runAbort()
					return err
				}
				w.log.Debugf("creating symlink %v --> %v", filepath.Join(sourceDir, sfName), path)
				symlink := filepath.Join(sourceDir, sfName)
				sfDir := filepath.Dir(symlink)
				if sfDir != "" {
					err = os.MkdirAll(sfDir, 0o755)
					if err != nil {
						w.log.Errorf("error during creating dir for symlink %v: %v", symlink, err)
						runAbort()
						return err
					}
					defer os.Remove(sfDir)
				}
				err = os.Symlink(path, symlink)
				if err != nil {
					w.log.Errorf("error during creating symlink: %v", err)
					runAbort()
					return err
				}
				unlockFiles = append(unlockFiles, unlock)
			}

			for _, tmpl := range spec.Cmds {

				rendered, err := tmpl.Render(build.JobContext{
					SourceDir: sourceDir,
					OutputDir: path,
					Deps:      depsMap,
				})

				if err != nil {
					err = fmt.Errorf("error during rendering cmd: %w", err)
					w.log.Error(err.Error())
					runAbort()
					return err
				}

				var cmd *exec.Cmd
				if tmpl.Exec != nil {

					cmd = exec.Command(rendered.Exec[0], rendered.Exec[1:]...)
					cmd.Env = rendered.Environ
					cmd.Dir = rendered.WorkingDirectory
				}

				if tmpl.CatTemplate != "" {
					cmd = exec.Command("sh", "-c", fmt.Sprintf("printf %q > %q", rendered.CatTemplate, rendered.CatOutput))
				}

				w.log.Debugf("cmd: %v", cmd.String())

				cmd.Stderr = &bytesErr
				cmd.Stdout = &bytesOut

				err = cmd.Run()
				if err != nil {
					err = fmt.Errorf("error during cmd %q running: %w", cmd.String(), err)
					w.log.Error(err.Error())
					return err
				}

				w.log.Debugf("err: %v, out: %v", bytesErr.String(), bytesOut.String())
			}

			finishedJobs = append(finishedJobs, api.JobResult{
				ID:       spec.ID,
				Stdout:   bytesOut.Bytes(),
				Stderr:   bytesErr.Bytes(),
				ExitCode: 0,
			})
			err = createArtifactCommit()
			if err != nil {
				err = fmt.Errorf("couldn't commit artifact creating: %w", err)
				w.log.Error(err.Error())
				return err
			}
		}
	}
}
