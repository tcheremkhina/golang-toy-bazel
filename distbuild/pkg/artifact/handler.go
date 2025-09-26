//go:build !solution

package artifact

import (
	"fmt"
	"net/http"

	"gitlab.com/slon/shad-go/distbuild/pkg/build"
	"gitlab.com/slon/shad-go/distbuild/pkg/tarstream"
	"go.uber.org/zap"
)

type Handler struct {
	logger *zap.Logger
	cache  *Cache
}

func NewHandler(l *zap.Logger, c *Cache) *Handler {
	return &Handler{l, c}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/artifact", func(w http.ResponseWriter, r *http.Request) {
		textID := r.Header.Get("id")
		var id build.ID
		if err := id.UnmarshalText([]byte(textID)); err != nil {
			http.Error(w, fmt.Sprintf("couldn't unmarshal id %q: %v", textID, err), http.StatusBadRequest)
		}
		h.logger.Debug("start handling", zap.String("requestURI", r.RequestURI), zap.String("id", id.String()))
		path, unlock, err := h.cache.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer unlock()
		if err = tarstream.Send(path, w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}
