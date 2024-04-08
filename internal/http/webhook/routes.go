package webhook

import (
	"net/http"
)

// routes wires the routes to handlers on a specific router.
func (h handler) routes(router *http.ServeMux) error {
	injectsidecar, err := h.injectSidecar()
	if err != nil {
		return err
	}
	router.Handle("/wh/mutating/injectsidecar", injectsidecar)

	return nil
}
