package slurm

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/d-hayashi/k8s-slurm-injector/internal/log"
	"github.com/d-hayashi/k8s-slurm-injector/internal/ssh_handler"
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
	handler  http.Handler
	ssh      ssh_handler.SSHHandler
	nodeInfo []nodeInfo
	logger   log.Logger
}

type nodeInfo struct {
	node      string
	partition string
}

// New returns a new webhook handler.
func New(config Config, sshHandler ssh_handler.SSHHandler) (http.Handler, error) {
	err := config.defaults()
	if err != nil {
		return nil, fmt.Errorf("handler configuration is not valid: %w", err)
	}

	mux := http.NewServeMux()

	h := handler{
		handler: mux,
		ssh:     sshHandler,
		logger:  config.Logger.WithKV(log.KV{"service": "slurm-handler"}),
	}

	// Get node information
	err = h.fetchSlurmNodeInfo()
	if err != nil {
		return nil, fmt.Errorf("could not fetch information of slurm-nodes: %w", err)
	}

	// Register all the routes with our router.
	err = h.routes(mux)
	if err != nil {
		return nil, fmt.Errorf("could not register routes on handler: %w", err)
	}

	return h, nil
}

func (h *handler) fetchSlurmNodeInfo() error {
	command := ssh_handler.SSHCommand{
		Command: "sinfo --Node | tail -n +2 | awk '{print $1,$3}'",
	}

	var _nodeInfo []nodeInfo
	out, err := h.ssh.RunCommand(command)
	candidates := strings.Split(string(out), "\n")
	for _, candidate := range candidates {
		info := strings.Split(candidate, " ")
		if len(info) != 2 {
			h.logger.Warningf("cannot deserialize string: %s, skipped", candidate)
			continue
		}
		node := info[0]
		partition := info[1]
		if node == "" {
			h.logger.Warningf("node is empty: %s, skipped", candidate)
			continue
		}
		if partition == "" {
			h.logger.Warningf("partition is empty: %s, skipped", candidate)
			continue
		}
		partition = strings.TrimSuffix(partition, "*")

		_nodeInfo = append(_nodeInfo, nodeInfo{node: node, partition: partition})
		h.logger.Debugf("registered a node: node=%s, partition=%s", node, partition)
	}

	if err == nil {
		h.nodeInfo = _nodeInfo
	}

	return err
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}
