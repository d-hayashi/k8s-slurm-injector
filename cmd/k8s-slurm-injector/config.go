package main

import (
	"os"
	"time"

	"gopkg.in/alecthomas/kingpin.v2"
)

// CmdConfig represents the configuration of the command.
type CmdConfig struct {
	Debug                   bool
	Development             bool
	WebhookListenAddr       string
	MetricsListenAddr       string
	SlurmListenAddr         string
	MetricsPath             string
	SlurmPath               string
	TLSCertFilePath         string
	TLSKeyFilePath          string
	EnableIngressSingleHost bool
	MinSMScrapeInterval     time.Duration

	SSHDestination   string
	SSHPort          string
	TargetNamespaces string
}

// NewCmdConfig returns a new command configuration.
func NewCmdConfig() (*CmdConfig, error) {
	c := &CmdConfig{}
	app := kingpin.New("k8s-slurm-injector", "A Kubernetes admission webhook for injecting slurm jobs.")
	app.Version(Version)

	app.Flag("debug", "Enable debug mode.").BoolVar(&c.Debug)
	app.Flag("development", "Enable development mode.").BoolVar(&c.Development)
	app.Flag("webhook-listen-address", "the address where the HTTPS server will be listening to serve the webhooks.").Default(":8080").StringVar(&c.WebhookListenAddr)
	app.Flag("metrics-listen-address", "the address where the HTTP server will be listening to serve metrics, healthchecks, profiling...").Default(":8081").StringVar(&c.MetricsListenAddr)
	app.Flag("slurm-listen-address", "the address where the HTTP server will be listening to serve slurm controls.").Default(":8082").StringVar(&c.SlurmListenAddr)
	app.Flag("metrics-path", "the path where Prometheus metrics will be served.").Default("/metrics").StringVar(&c.MetricsPath)
	app.Flag("slurm-path", "the path where slurm-controls will be served.").Default("/slurm").StringVar(&c.SlurmPath)
	app.Flag("tls-cert-file-path", "the path for the webhook HTTPS server TLS cert file.").StringVar(&c.TLSCertFilePath)
	app.Flag("tls-key-file-path", "the path for the webhook HTTPS server TLS key file.").StringVar(&c.TLSKeyFilePath)
	app.Flag("webhook-ssh-destination", "SSH destination where slurm controller is working").StringVar(&c.SSHDestination)
	app.Flag("webhook-ssh-port", "SSH port").Default("22").StringVar(&c.SSHPort)
	app.Flag("target-namespaces", "Target namespaces (comma separated)").StringVar(&c.TargetNamespaces)
	app.Flag("webhook-sm-min-scrape-interval", "the minimum screate interval service monitors can have.").DurationVar(&c.MinSMScrapeInterval)

	_, err := app.Parse(os.Args[1:])
	if err != nil {
		return nil, err
	}

	return c, nil
}
