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

	finalizer, err := h.initFinalizer()
	if err != nil {
		return err
	}
	router.Handle("/wh/mutating/finalize", finalizer)

	allmark, err := h.allMark()
	if err != nil {
		return err
	}
	router.Handle("/wh/mutating/allmark", allmark)

	ingressVal, err := h.ingressValidation()
	if err != nil {
		return err
	}
	router.Handle("/wh/validating/ingress", ingressVal)

	safeServiceMonitor, err := h.safeServiceMonitor()
	if err != nil {
		return err
	}
	router.Handle("/wh/mutating/safeservicemonitor", safeServiceMonitor)

	return nil
}
