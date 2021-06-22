package webhook

import (
	"fmt"
	"net/http"

	"github.com/d-hayashi/k8s-slurm-injector/internal/log"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/finalizer"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
)

// Config is the handler configuration.
type Config struct {
	MetricsRecorder MetricsRecorder
	SidecarInjector sidecar.SidecarInjector
	Finalizer       finalizer.Finalizer
	Logger          log.Logger
}

func (c *Config) defaults() error {

	if c.SidecarInjector == nil {
		return fmt.Errorf("sidecar injector is required")
	}

	if c.Finalizer == nil {
		return fmt.Errorf("finalizer is required")
	}

	if c.MetricsRecorder == nil {
		c.MetricsRecorder = dummyMetricsRecorder
	}

	if c.Logger == nil {
		c.Logger = log.Dummy
	}

	return nil
}

type handler struct {
	sidecar   sidecar.SidecarInjector
	finalizer finalizer.Finalizer
	handler   http.Handler
	metrics   MetricsRecorder
	logger    log.Logger
}

// New returns a new webhook handler.
func New(config Config) (http.Handler, error) {
	err := config.defaults()
	if err != nil {
		return nil, fmt.Errorf("handler configuration is not valid: %w", err)
	}

	mux := http.NewServeMux()

	h := handler{
		handler:   mux,
		sidecar:   config.SidecarInjector,
		finalizer: config.Finalizer,
		metrics:   config.MetricsRecorder,
		logger:    config.Logger.WithKV(log.KV{"service": "webhook-handler"}),
	}

	// Register all the routes with our router.
	err = h.routes(mux)
	if err != nil {
		return nil, fmt.Errorf("could not register routes on handler: %w", err)
	}

	// Register root handler middlware.
	h.handler = h.measuredHandler(h.handler) // Add metrics middleware.

	return h, nil
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}
