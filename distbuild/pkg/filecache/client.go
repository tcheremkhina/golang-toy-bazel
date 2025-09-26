//go:build !solution

package filecache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"go.uber.org/zap"

	"gitlab.com/slon/shad-go/distbuild/pkg/build"
)

type Client struct {
	logger   *zap.Logger
	endpoint string
	client   *http.Client
}

func NewClient(l *zap.Logger, endpoint string) *Client {
	return &Client{l, endpoint, &http.Client{}}
}

func (c *Client) Upload(ctx context.Context, id build.ID, localPath string) error {
	content, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("couldn't read file %v: %w", localPath, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.endpoint+"/file", bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("error during creating request: %w", err)
	}

	req.Header.Set("id", id.String())

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("error during /file request: %w", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("bad response from /file file_id: %v, status_code: %v", id, resp.StatusCode)
		}
		return fmt.Errorf("bad response from /file file_id: %v, status_code: %v, body: %v", id, resp.StatusCode, string(buf))
	}

	return nil
}

func (c *Client) Download(ctx context.Context, localCache *Cache, id build.ID) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/file", nil)
	if err != nil {
		err = fmt.Errorf("error during creating /file request: %w", err)
		c.logger.Error(err.Error())
		return err
	}
	req.Header.Set("id", id.String())

	resp, err := c.client.Do(req)
	if err != nil {
		err = fmt.Errorf("error during /file request: %w", err)
		c.logger.Error(err.Error())
		return err
	}

	buf, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		err = fmt.Errorf("error during reading /file response: %w", err)
		c.logger.Error(err.Error())
		return err
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.Error(
			"/file request returned with error",
			zap.Int("status_code", resp.StatusCode),
			zap.String("error", string(buf)),
		)
		return errors.New(string(buf))
	}

	writeCloser, abort, err := localCache.Write(id)
	if err != nil {
		err = fmt.Errorf("error during adding file to local cache: %w", err)
		c.logger.Error(err.Error())
		return err
	}
	defer writeCloser.Close()

	_, err = writeCloser.Write(buf)
	if err != nil {
		err = fmt.Errorf("error during writing file %v content to local cache: %w", id, err)
		c.logger.Error(err.Error())
		abortErr := abort()
		if abortErr != nil {
			c.logger.Error("couldn't abort writing file", zap.String("file_id", id.String()), zap.Error(abortErr))
		}
		return err
	}

	return nil
}
