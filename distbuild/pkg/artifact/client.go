//go:build !solution

package artifact

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"gitlab.com/slon/shad-go/distbuild/pkg/build"
	"gitlab.com/slon/shad-go/distbuild/pkg/tarstream"
)

// Download artifact from remote cache into local cache.
func Download(ctx context.Context, endpoint string, c *Cache, artifactID build.ID) error {
	client := http.Client{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/artifact", nil)
	if err != nil {
		return fmt.Errorf("error during creating request: %w", err)
	}
	req.Header.Set("id", artifactID.String())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error during /artifact request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("/artifact request finished with status_code %v, couldn't read body", resp.StatusCode)
		}
		return fmt.Errorf("/artifact reuest finished with status_code: %v, err: %v", resp.StatusCode, string(buf))
	}

	path, commit, abort, err := c.Create(artifactID)
	if err != nil {
		return fmt.Errorf("error during adding artifact %v to local cache: %w", artifactID, err)
	}

	if err = tarstream.Receive(path, resp.Body); err != nil {
		aboortErr := abort()
		if aboortErr != nil {
			return fmt.Errorf("error during receiving artifact %v: %w; also error during aborting: %v", artifactID, err, aboortErr)
		}
		return fmt.Errorf("error during receiving artifact %v: %w", artifactID, err)
	}

	if err = commit(); err != nil {
		return fmt.Errorf("error during committing artifact %v to local cache: %w", artifactID, err)
	}
	return nil
}
