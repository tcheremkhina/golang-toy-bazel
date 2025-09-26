//go:build !solution

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/zap"
)

type HeartbeatClient struct {
	logger   *zap.Logger
	endpoint string
	client   *http.Client
}

func NewHeartbeatClient(l *zap.Logger, endpoint string) *HeartbeatClient {
	return &HeartbeatClient{l, endpoint, &http.Client{}}
}

func (c *HeartbeatClient) Heartbeat(ctx context.Context, req *HeartbeatRequest) (*HeartbeatResponse, error) {
	buf, err := json.Marshal(req)
	if err != nil {
		err = fmt.Errorf("error during marshaling /heartbeat request json: %w", err)
		c.logger.Error(err.Error())
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/heartbeat", bytes.NewBuffer(buf))
	if err != nil {
		err = fmt.Errorf("error during making /heartbeat request: %w", err)
		c.logger.Error(err.Error())
		return nil, err
	}

	resp, err := c.client.Do(request)
	if err != nil {
		err = fmt.Errorf("error during /heartbeat request: %w", err)
		c.logger.Error(err.Error())
		return nil, err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errorText string
		err = decoder.Decode(&errorText)
		if err != nil {
			err = fmt.Errorf("error during decording /heartbeat error text: %w", err)
			c.logger.Error(err.Error())
			return nil, err
		}
		c.logger.Warn("got /heartbeat response", zap.Int("status_code", resp.StatusCode), zap.String("error", errorText))
		return nil, errors.New(errorText)
	}

	var response HeartbeatResponse
	err = decoder.Decode(&response)
	if err != nil {
		err = fmt.Errorf("error during decoding /heartbeat response: %w", err)
		c.logger.Error(err.Error())
		return nil, err
	}
	c.logger.Debug("got /heartbeat response", zap.Int("status_code", resp.StatusCode), zap.Any("body", response))
	return &response, nil
}
