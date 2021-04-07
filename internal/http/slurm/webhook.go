package slurm

import (
	"fmt"
	"net/http"

	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/log"
	"github.com/d-hayashi/k8s-slurm-injector/internal/slurm_handler"
)

// Config is the handler configuration.
type Config struct {
	Logger log.Logger
}

func (c *Config) defaults() error {
	if c.Logger == nil {
		c.Logger = log.Dummy
	}

	return nil
}

type Handler interface {
	fetchSlurmNodes() error
	fetchSlurmPartitions() error
}

type handler struct {
	handler          http.Handler
	slurmHandler     slurm_handler.SlurmHandler
	configMapHandler config_map.ConfigMapHandler
	logger           log.Logger
}

// New returns a new webhook handler.
func New(config Config, configMapHandler config_map.ConfigMapHandler, slurmHandler slurm_handler.SlurmHandler) (http.Handler, error) {
	err := config.defaults()
	if err != nil {
		return nil, fmt.Errorf("handler configuration is not valid: %w", err)
	}

	mux := http.NewServeMux()

	h := handler{
		handler:          mux,
		slurmHandler:     slurmHandler,
		configMapHandler: configMapHandler,
		logger:           config.Logger.WithKV(log.KV{"service": "http-slurm"}),
	}

	// Register all the routes with our router.
	err = h.routes(mux)
	if err != nil {
		return nil, fmt.Errorf("could not register routes on handler: %w", err)
	}

	return h, nil
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}
