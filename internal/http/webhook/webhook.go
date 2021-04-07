package webhook

import (
	"fmt"
	"net/http"

	"github.com/d-hayashi/k8s-slurm-injector/internal/log"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/finalizer"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/mark"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/prometheus"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	"github.com/d-hayashi/k8s-slurm-injector/internal/validation/ingress"
)

// Config is the handler configuration.
type Config struct {
	MetricsRecorder            MetricsRecorder
	Marker                     mark.Marker
	SidecarInjector            sidecar.SidecarInjector
	Finalizer                  finalizer.Finalizer
	IngressRegexHostValidator  ingress.Validator
	IngressSingleHostValidator ingress.Validator
	ServiceMonitorSafer        prometheus.ServiceMonitorSafer
	Logger                     log.Logger
}

func (c *Config) defaults() error {
	if c.Marker == nil {
		return fmt.Errorf("marker is required")
	}

	if c.SidecarInjector == nil {
		return fmt.Errorf("sidecar injector is required")
	}

	if c.Finalizer == nil {
		return fmt.Errorf("finalizer is required")
	}

	if c.IngressRegexHostValidator == nil {
		return fmt.Errorf("ingress regex host validator is required")
	}

	if c.IngressSingleHostValidator == nil {
		return fmt.Errorf("ingress single host validator is required")
	}

	if c.ServiceMonitorSafer == nil {
		return fmt.Errorf("service monitor safer is required")
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
	marker           mark.Marker
	sidecar          sidecar.SidecarInjector
	finalizer        finalizer.Finalizer
	ingRegexHostVal  ingress.Validator
	ingSingleHostVal ingress.Validator
	servMonSafer     prometheus.ServiceMonitorSafer
	handler          http.Handler
	metrics          MetricsRecorder
	logger           log.Logger
}

// New returns a new webhook handler.
func New(config Config) (http.Handler, error) {
	err := config.defaults()
	if err != nil {
		return nil, fmt.Errorf("handler configuration is not valid: %w", err)
	}

	mux := http.NewServeMux()

	h := handler{
		handler:          mux,
		marker:           config.Marker,
		sidecar:          config.SidecarInjector,
		finalizer:        config.Finalizer,
		ingRegexHostVal:  config.IngressRegexHostValidator,
		ingSingleHostVal: config.IngressSingleHostValidator,
		servMonSafer:     config.ServiceMonitorSafer,
		metrics:          config.MetricsRecorder,
		logger:           config.Logger.WithKV(log.KV{"service": "webhook-handler"}),
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
