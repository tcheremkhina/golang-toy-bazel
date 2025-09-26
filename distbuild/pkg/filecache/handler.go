//go:build !solution

package filecache

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"gitlab.com/slon/shad-go/distbuild/pkg/build"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

type Handler struct {
	logger *zap.Logger
	cache  *Cache
	g      *singleflight.Group
}

func NewHandler(l *zap.Logger, cache *Cache) *Handler {
	return &Handler{l, cache, &singleflight.Group{}}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		var id build.ID
		if err := id.UnmarshalText([]byte(r.Header.Get("id"))); err != nil {
			err = fmt.Errorf("error diring unmarshaling file id: %w", err)
			h.logger.Error(err.Error(), zap.String("requestURI", r.RequestURI), zap.String("method", r.Method))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		h.logger.Debug("start handling", zap.String("requestURI", r.RequestURI), zap.String("method", r.Method), zap.String("file_id", id.String()))

		switch r.Method {
		case http.MethodGet:

			path, unlock, err := h.cache.Get(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer unlock()

			data, err := os.ReadFile(path)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			fmt.Fprintf(w, "%v", string(data))
			return

		case http.MethodPut:
			// use singleflight to avoid multiple uploads of the same file
			f := func() (any, error) {
				writeCloser, abort, err := h.cache.Write(id)
				if err != nil {
					if errors.Is(err, ErrExists) {
						h.logger.Debug("file already exists; skip uploading", zap.String("file_id", id.String()))
						return nil, nil
					}
					return nil, err
				}
				buf, err := io.ReadAll(r.Body)
				r.Body.Close()
				if err != nil {
					abortErr := abort()
					if abortErr != nil {
						h.logger.Error("error during aborting write", zap.Error(abortErr))
						return nil, fmt.Errorf("error during reading body: %w; error during aborting write: %v", err, abortErr)
					}
					return nil, fmt.Errorf("error during aborting write: %w", err)
				}
				_, err = writeCloser.Write(buf)
				if err != nil {
					h.logger.Error("error during writing to cache", zap.Error(err))
					abortErr := abort()
					if abortErr != nil {
						h.logger.Error("error during aborting write", zap.Error(abortErr))
						return nil, fmt.Errorf("error during writing to cache: %w; error during aborting write: %v", err, abortErr)
					}
					return nil, fmt.Errorf("couldn't write file to local cache: %w", err)
				}
				err = writeCloser.Close()
				if err != nil {
					return nil, fmt.Errorf("couldn't close file writer: %w", err)
				}
				return nil, nil
			}
			_, err, _ := h.g.Do(id.String(), f)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}
	})
}
