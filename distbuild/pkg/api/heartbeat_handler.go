//go:build !solution

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"go.uber.org/zap"
)

type HeartbeatHandler struct {
	l *zap.Logger
	s HeartbeatService
}

func NewHeartbeatHandler(l *zap.Logger, s HeartbeatService) *HeartbeatHandler {
	return &HeartbeatHandler{l, s}
}

func (h *HeartbeatHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			err = fmt.Errorf("error during reading body of /heartbeat request: %w", err)
			h.l.Error(err.Error())
			http.Error(w, fmt.Sprintf("%q\n", err.Error()), http.StatusInternalServerError)
			return
		}
		r.Body.Close()
		h.l.Debug("start handling", zap.String("requestURI", r.RequestURI), zap.String("body", string(b)))

		var req HeartbeatRequest
		err = json.Unmarshal(b, &req)
		if err != nil {
			err = fmt.Errorf("error during unmarshaling /heartbeat request: %w", err)
			h.l.Error(err.Error())
			http.Error(w, fmt.Sprintf("%q\n", err), http.StatusBadRequest)
			return
		}

		resp, err := h.s.Heartbeat(r.Context(), &req)
		if err != nil {
			h.l.Error("error during Heartbeat", zap.Error(err))
			http.Error(w, fmt.Sprintf("%q\n", err), http.StatusInternalServerError)
			return
		}

		respText, err := json.Marshal(resp)
		if err != nil {
			err = fmt.Errorf("error during marshaling /heartbeat response json: %w", err)
			h.l.Error(err.Error())
			http.Error(w, fmt.Sprintf("%q\n", err), http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "%v\n", string(respText))
	})
}
