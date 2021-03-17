package slurm

import (
	"fmt"
	"net/http"

	"github.com/d-hayashi/k8s-slurm-injector/internal/log"
)

// Config is the handler configuration.
type Config struct {
	SSHDestination string
	SSHPort        string
	Logger         log.Logger
}

func (c *Config) defaults() error {
	if c.Logger == nil {
		c.Logger = log.Dummy
	}

	return nil
}

type handler struct {
	handler        http.Handler
	sshDestination string
	sshPort        string
	logger         log.Logger
}

// New returns a new webhook handler.
func New(config Config) (http.Handler, error) {
	err := config.defaults()
	if err != nil {
		return nil, fmt.Errorf("handler configuration is not valid: %w", err)
	}

	mux := http.NewServeMux()

	h := handler{
		handler:        mux,
		sshDestination: config.SSHDestination,
		sshPort:        config.SSHPort,
		logger:         config.Logger.WithKV(log.KV{"service": "slurm-handler"}),
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
