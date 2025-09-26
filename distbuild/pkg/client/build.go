//go:build !solution

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"go.uber.org/zap"

	"gitlab.com/slon/shad-go/distbuild/pkg/api"
	"gitlab.com/slon/shad-go/distbuild/pkg/build"
	"gitlab.com/slon/shad-go/distbuild/pkg/filecache"
)

type Client struct {
	l         *zap.Logger
	sourceDir string
	client    *api.BuildClient
	filecache *filecache.Client
}

func NewClient(
	l *zap.Logger,
	apiEndpoint string,
	sourceDir string,
) *Client {
	return &Client{l, sourceDir, api.NewBuildClient(l, apiEndpoint), filecache.NewClient(l, apiEndpoint)}
}

type BuildListener interface {
	OnJobStdout(jobID build.ID, stdout []byte) error
	OnJobStderr(jobID build.ID, stderr []byte) error

	OnJobFinished(jobID build.ID) error
	OnJobFailed(jobID build.ID, code int, error string) error
}

func (c *Client) Build(ctx context.Context, graph build.Graph, lsn BuildListener) error {
	build, statusReader, err := c.client.StartBuild(ctx, &api.BuildRequest{Graph: graph})
	if err != nil {
		err = fmt.Errorf("couldn't start build: %w", err)
		c.l.Error(err.Error())
		return err
	}
	defer statusReader.Close()

	c.l.Debug("new build started", zap.Any("build", *build))

	for _, id := range build.MissingFiles {
		path := graph.SourceFiles[id]
		err = c.filecache.Upload(ctx, id, filepath.Join(c.sourceDir, path))
		if err != nil {
			err = fmt.Errorf("error during Uploading file %v to filecache: %w", id, err)
			c.l.Error(err.Error())
			return err
		}
	}

	_, err = c.client.SignalBuild(ctx, build.ID, &api.SignalRequest{UploadDone: &api.UploadDone{}})
	if err != nil {
		err = fmt.Errorf("error during SignalBuild request, couldn't send UploadDone signal: %w", err)
		c.l.Error(err.Error())
		return err
	}

	for {
		upd, err := statusReader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) && upd != nil {
				c.l.Info("finish build process as found EOF in status reader", zap.String("build_id", build.ID.String()))
				break
			} else {
				c.l.Error("error during reading next status of build", zap.String("build_id", build.ID.String()), zap.Error(err))
				return err
			}
		}
		if upd.BuildFinished != nil {
			c.l.Info("build finished, found BuildFinished status", zap.String("build_id", build.ID.String()))
			break
		}
		if upd.BuildFailed != nil {
			c.l.Info("build failed, found BildFailed status", zap.String("build_id", build.ID.String()))
			return errors.New(upd.BuildFailed.Error)
		}
		if finished := upd.JobFinished; finished != nil {
			if err := lsn.OnJobStdout(finished.ID, finished.Stdout); err != nil {
				c.l.Error("job stdout handler finished with error", zap.String("job_id", finished.ID.String()), zap.Error(err))
			}
			if err := lsn.OnJobStderr(finished.ID, finished.Stderr); err != nil {
				c.l.Error("job stderr handler finished with error", zap.String("job_id", finished.ID.String()), zap.Error(err))
			}
			if finished.Error != nil {
				if err := lsn.OnJobFailed(finished.ID, finished.ExitCode, *finished.Error); err != nil {
					c.l.Error("job result handler finished with error", zap.String("job_id", finished.ID.String()), zap.Error(err))
				}
			} else {
				if err := lsn.OnJobFinished(upd.JobFinished.ID); err != nil {
					c.l.Error("job result handler finished with error", zap.String("job_id", finished.ID.String()), zap.Error(err))
				}
			}
		}
	}
	return nil
}
