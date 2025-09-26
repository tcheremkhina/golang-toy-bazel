//go:build !solution

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	"gitlab.com/slon/shad-go/distbuild/pkg/build"
	"go.uber.org/zap"
)

type MyStatusWriter struct {
	upds chan *StatusUpdate

	ctrl *http.ResponseController
	w    http.ResponseWriter

	startedWriting atomic.Bool
	buildFinished  atomic.Bool
	buildStarted   atomic.Bool
	closed         atomic.Bool
}

func (w *MyStatusWriter) Started(rsp *BuildStarted) error {
	if w.startedWriting.CompareAndSwap(false, true) {

		jsonData, err := json.Marshal(*rsp)
		if err != nil {
			return err
		}
		fmt.Fprintf(w.w, "%v\n", string(jsonData))
		w.ctrl.Flush()
		return nil
	} else {
		return errors.New("started after failed")
	}
}

func (w *MyStatusWriter) Updated(u *StatusUpdate) error {
	if w.closed.Load() {
		return errors.New("update on closed writer")
	}
	w.upds <- u
	if u.BuildFailed != nil || u.BuildFinished != nil {
		w.buildFinished.Store(true)
	}
	if w.buildStarted.Load() && w.buildFinished.Load() && w.closed.CompareAndSwap(false, true) {
		close(w.upds)
	}
	return nil
}

func handleUpdated(update *StatusUpdate, w http.ResponseWriter, ctrl *http.ResponseController) error {
	message, err := json.Marshal(update)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "%v\n", string(message))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		ctrl.Flush()
		return err
	}
	ctrl.Flush()
	return nil
}

func NewBuildService(l *zap.Logger, s Service) *BuildHandler {
	return &BuildHandler{l, s}
}

type BuildHandler struct {
	l *zap.Logger
	s Service
}

func readBody(l *zap.Logger, requestName string, body io.ReadCloser, w http.ResponseWriter) ([]byte, bool) {
	b, err := io.ReadAll(body)
	if err != nil {
		errMessage := fmt.Sprintf("error during reading body of %v request", requestName)
		l.Error(errMessage)

		http.Error(w, fmt.Sprintf("error during reading body: %v", err), http.StatusInternalServerError)
		return nil, false
	}
	body.Close()
	return b, true
}

func (h *BuildHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/build", func(w http.ResponseWriter, r *http.Request) {
		b, ok := readBody(h.l, "/build", r.Body, w)
		if !ok {
			return
		}

		h.l.Debug("start handling", zap.String("requestURI", r.RequestURI), zap.String("body", string(b)))

		var req BuildRequest
		err := json.Unmarshal(b, &req)
		if err != nil {
			http.Error(w, fmt.Sprintf("error during unmarshaling request json: %v", err), http.StatusBadRequest)
			return
		}

		rc := http.NewResponseController(w)

		sw := MyStatusWriter{upds: make(chan *StatusUpdate, 100), ctrl: rc, w: w}
		err = h.s.StartBuild(r.Context(), &req, &sw)

		if err != nil {
			if sw.startedWriting.CompareAndSwap(false, true) {
				// statusWriter was not opened, return error from handler
				h.l.Error("StartBuild returned error", zap.Error(err))

				http.Error(w, fmt.Sprintf("%q\n", err.Error()), http.StatusInternalServerError)
				rc.Flush()
				return
			} else {
				// statusWriter opened, send error as update status
				updErr := sw.Updated(&StatusUpdate{nil, &BuildFailed{err.Error()}, nil})
				if updErr != nil {
					errMessage := fmt.Sprintf("error during updating status BuildFailed: %v", updErr)
					h.l.Error(errMessage)

					http.Error(w, fmt.Sprintf("%q\n", errMessage), http.StatusInternalServerError)
					rc.Flush()
					return
				}
				h.l.Error("error during StartBuild, sent as BuildFailed status", zap.Error(err))
				// do not write anything to response; error will be sent with Updated(BuildFailed)
			}
		}

		sw.buildStarted.Store(true)
		if sw.buildFinished.Load() && sw.closed.CompareAndSwap(false, true) {
			// all messages sent, close channel
			close(sw.upds)
		}

		for {
			select {
			case upd := <-sw.upds:
				if upd == nil { // channel closed
					return
				}
				if err := handleUpdated(upd, w, rc); err != nil {
					h.l.Error(fmt.Sprintf("error during handle update status: %v", err))
				}

			case <-r.Context().Done():
				h.l.Debug("build cancelled, close connection")
				return
			}
		}
	})

	mux.HandleFunc("/signal", func(w http.ResponseWriter, r *http.Request) {
		body, ok := readBody(h.l, "/signal", r.Body, w)
		if !ok {
			return
		}

		h.l.Debug("start handling", zap.String("requestURI", r.RequestURI), zap.String("body", string(body)))

		var signal SignalRequest
		if err := json.Unmarshal(body, &signal); err != nil {
			errMessage := fmt.Sprintf("error during json unmarshal: %v", err)
			h.l.Error(errMessage)
			http.Error(w, fmt.Sprintf("%q\n", errMessage), http.StatusBadRequest)
			return
		}

		var id build.ID
		textID := r.Header.Get("build_id")
		if err := id.UnmarshalText([]byte(textID)); err != nil {
			http.Error(w, fmt.Sprintf("couldn't unmarshal build_id %q: %v", textID, err), http.StatusBadRequest)
		}

		sigResp, err := h.s.SignalBuild(r.Context(), id, &signal)
		if err != nil {
			http.Error(w, fmt.Sprintf("%q\n", err.Error()), http.StatusInternalServerError)
			return
		}

		resp, err := json.Marshal(sigResp)
		if err != nil {
			http.Error(w, fmt.Sprintf("%q\n", err.Error()), http.StatusInternalServerError)
			return
		}
		if _, err = w.Write(resp); err != nil {
			err = fmt.Errorf("error during writing to response: %w", err)
			h.l.Error(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}
