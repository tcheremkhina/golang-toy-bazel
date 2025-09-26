//go:build !solution

package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"gitlab.com/slon/shad-go/distbuild/pkg/build"
)

type BuildClient struct {
	logger   *zap.Logger
	endpoint string
	httpc    *http.Client
}

type MyStatusReader struct {
	d *json.Decoder
	r *http.Response
}

func NewStatusReader(r *http.Response) *MyStatusReader {
	reader := bufio.NewReader(r.Body)
	d := json.NewDecoder(reader)
	return &MyStatusReader{d, r}
}

func (s *MyStatusReader) Next() (*StatusUpdate, error) {
	var upd StatusUpdate
	err := s.d.Decode(&upd)
	return &upd, err
}

func (s *MyStatusReader) Close() error {
	return s.r.Body.Close()
}

func NewBuildClient(l *zap.Logger, endpoint string) *BuildClient {
	return &BuildClient{logger: l, endpoint: endpoint, httpc: &http.Client{Timeout: 30 * time.Second}}
}

func (c *BuildClient) StartBuild(ctx context.Context, request *BuildRequest) (*BuildStarted, StatusReader, error) {
	jsonData, err := json.Marshal(*request)
	if err != nil {
		err = fmt.Errorf("error during unmarshaling request json: %w", err)
		c.logger.Error(err.Error(), zap.Any("request", *request))
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/build", bytes.NewBuffer(jsonData))
	if err != nil {
		err = fmt.Errorf("error during /build request making: %w", err)
		c.logger.Error(err.Error(), zap.Any("request", *request))
		return nil, nil, err
	}

	resp, err := c.httpc.Do(req)

	if err != nil {
		err = fmt.Errorf("SrartBuild request failed: %w", err)
		c.logger.Error(err.Error())
		return nil, nil, err
	}

	decoder := json.NewDecoder(resp.Body)

	handleReadingBodyError := func(err error) error { // requires err != nil
		err = fmt.Errorf("error during reading response body: %w", err)
		c.logger.Error(err.Error())
		return err
	}

	if resp.StatusCode != http.StatusOK {
		var errText string
		err = decoder.Decode(&errText)
		resp.Body.Close()
		if err != nil {
			return nil, nil, handleReadingBodyError(err)
		}

		c.logger.Error("start build request failed", zap.Int("status_code", resp.StatusCode), zap.String("error", errText))

		return nil, nil, errors.New(errText)
	}

	var buildStarted BuildStarted
	err = decoder.Decode(&buildStarted)
	if err != nil {
		resp.Body.Close()
		return nil, nil, handleReadingBodyError(err)
	}

	c.logger.Debug("got response from /build started", zap.Int("status_code", resp.StatusCode), zap.Any("response", buildStarted))

	return &buildStarted, &MyStatusReader{decoder, resp}, nil
}

func (c *BuildClient) SignalBuild(ctx context.Context, buildID build.ID, signal *SignalRequest) (*SignalResponse, error) {
	jsonData, err := json.Marshal(*signal)
	if err != nil {
		return nil, errors.New("error during marshaling signal response")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/signal", bytes.NewBuffer(jsonData))
	if err != nil {
		err = fmt.Errorf("error during making /signal request")
		return nil, err
	}
	req.Header.Set("build_id", buildID.String())

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("error during reading response body from /signal", zap.Error(err))
		return nil, err
	}

	c.logger.Debug("got response from /signal", zap.Int("status_code", resp.StatusCode), zap.Any("response_body", string(buf)))

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("signal build request failed", zap.Int("status_code", resp.StatusCode))
		var errText string
		err := json.Unmarshal(buf, &errText)
		if err != nil {
			c.logger.Error("error during unmarshalings error response body", zap.Error(err))
			return nil, errors.New("internal error")
		}
		return nil, errors.New(errText)
	}

	var signalResp SignalResponse
	if err := json.Unmarshal(buf, &signalResp); err != nil {
		err = fmt.Errorf("error during unmarshaling response json: %w", err)
		c.logger.Error(err.Error())
		return nil, err
	}

	return &signalResp, nil
}
