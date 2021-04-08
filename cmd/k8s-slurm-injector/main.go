package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/http/slurm"
	"github.com/d-hayashi/k8s-slurm-injector/internal/http/webhook"
	"github.com/d-hayashi/k8s-slurm-injector/internal/log"
	internalmetricsprometheus "github.com/d-hayashi/k8s-slurm-injector/internal/metrics/prometheus"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/finalizer"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/mark"
	internalmutationprometheus "github.com/d-hayashi/k8s-slurm-injector/internal/mutation/prometheus"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	"github.com/d-hayashi/k8s-slurm-injector/internal/slurm_handler"
	"github.com/d-hayashi/k8s-slurm-injector/internal/ssh_handler"
	"github.com/d-hayashi/k8s-slurm-injector/internal/validation/ingress"
	"github.com/d-hayashi/k8s-slurm-injector/internal/watcher"
)

var (
	// Version is set at compile time.
	Version = "dev"
)

func runApp() error {
	cfg, err := NewCmdConfig()
	if err != nil {
		return fmt.Errorf("could not get commandline configuration: %w", err)
	}

	// Set up logger.
	logrusLog := logrus.New()
	logrusLogEntry := logrus.NewEntry(logrusLog).WithField("app", "k8s-slurm-injector")
	if cfg.Debug {
		logrusLogEntry.Logger.SetLevel(logrus.DebugLevel)
	}
	if !cfg.Development {
		logrusLogEntry.Logger.SetFormatter(&logrus.JSONFormatter{})
	}
	logger := log.NewLogrus(logrusLogEntry).WithKV(log.KV{"version": Version})
	logger.Debugf("debug mode")

	// Dependencies.
	metricsRec := internalmetricsprometheus.NewRecorder(prometheus.DefaultRegisterer)

	// Establish SSH connection
	sshHandler, err := ssh_handler.New(cfg.SSHDestination, cfg.SSHPort)
	if err != nil {
		return fmt.Errorf("failed to esablish SSH connection: %s", err)
	}

	// Initialize config-map-handler
	configMapHandler, err := config_map.NewConfigMapHandler()
	if err != nil {
		return fmt.Errorf("failed to initialize config-map-handler: %s", err)
	}

	// Initialize slurm-handler
	slurmHandler, err := slurm_handler.NewSlurmHandler(sshHandler)
	if err != nil {
		return fmt.Errorf("failed to initialize slurm-handler: %s", err)
	}

	// Initialize finalizer
	_finalizer, err := finalizer.NewFinalizer(configMapHandler, slurmHandler)
	if err != nil {
		return fmt.Errorf("failed to initialize finalizer: %s", err)
	}

	sidecarInjector, err := sidecar.NewSidecarInjector(sshHandler)
	if err != nil {
		return fmt.Errorf("failed to initialize sidecar injector: %w", err)
	}

	var marker mark.Marker
	if len(cfg.LabelMarks) > 0 {
		marker = mark.NewLabelMarker(cfg.LabelMarks)
		logger.Infof("label marker webhook enabled")
	} else {
		marker = mark.DummyMarker
		logger.Warningf("label marker webhook disabled")
	}

	var ingressHostValidator ingress.Validator
	if len(cfg.IngressHostRegexes) > 0 {
		ingressHostValidator, err = ingress.NewHostRegexValidator(cfg.IngressHostRegexes)
		if err != nil {
			return fmt.Errorf("could not create ingress regex host validator: %w", err)
		}
		logger.Infof("ingress host regex validation webhook enabled")
	} else {
		ingressHostValidator = ingress.DummyValidator
		logger.Warningf("ingress host regex validation webhook disabled")
	}

	var ingressSingleHostValidator ingress.Validator
	if cfg.EnableIngressSingleHost {
		ingressSingleHostValidator = ingress.SingleHostValidator
		logger.Infof("ingress single host validation webhook enabled")
	} else {
		ingressSingleHostValidator = ingress.DummyValidator
		logger.Warningf("ingress single host validation webhook disabled")
	}

	var serviceMonitorSafer internalmutationprometheus.ServiceMonitorSafer
	if cfg.MinSMScrapeInterval != 0 {
		serviceMonitorSafer = internalmutationprometheus.NewServiceMonitorSafer(cfg.MinSMScrapeInterval)
		logger.Infof("service monitor safer webhook enabled")
	} else {
		serviceMonitorSafer = internalmutationprometheus.DummyServiceMonitorSafer
		logger.Warningf("service monitor safer webhook disabled")
	}

	// Prepare run entrypoints.
	var g run.Group

	// OS signals.
	{
		sigC := make(chan os.Signal, 1)
		exitC := make(chan struct{})
		signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)

		g.Add(
			func() error {
				select {
				case s := <-sigC:
					logger.Infof("signal %s received", s)
					return nil
				case <-exitC:
					return nil
				}
			},
			func(_ error) {
				close(exitC)
			},
		)
	}

	// Metrics HTTP server.
	{
		logger := logger.WithKV(log.KV{"addr": cfg.MetricsListenAddr, "http-server": "metrics"})
		mux := http.NewServeMux()

		// Metrics.
		mux.Handle(cfg.MetricsPath, promhttp.Handler())

		// Pprof.
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

		// Health checks.
		mux.HandleFunc("/healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

		server := http.Server{Addr: cfg.MetricsListenAddr, Handler: mux}

		g.Add(
			func() error {
				logger.Infof("http server listening...")
				return server.ListenAndServe()
			},
			func(_ error) {
				logger.Infof("start draining connections")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				err := server.Shutdown(ctx)
				if err != nil {
					logger.Errorf("error while shutting down the server: %s", err)
				} else {
					logger.Infof("server stopped")
				}
			},
		)
	}

	// Webhook HTTP server.
	{
		logger := logger.WithKV(log.KV{"addr": cfg.WebhookListenAddr, "http-server": "webhooks"})

		// Webhook handler.
		wh, err := webhook.New(webhook.Config{
			Marker:                     marker,
			SidecarInjector:            sidecarInjector,
			Finalizer:                  _finalizer,
			IngressRegexHostValidator:  ingressHostValidator,
			IngressSingleHostValidator: ingressSingleHostValidator,
			ServiceMonitorSafer:        serviceMonitorSafer,
			MetricsRecorder:            metricsRec,
			Logger:                     logger,
		})
		if err != nil {
			return fmt.Errorf("could not create webhooks handler: %w", err)
		}

		mux := http.NewServeMux()
		mux.Handle("/", wh)
		server := http.Server{Addr: cfg.WebhookListenAddr, Handler: mux}

		g.Add(
			func() error {
				if cfg.TLSCertFilePath == "" || cfg.TLSKeyFilePath == "" {
					logger.Warningf("webhook running without TLS")
					logger.Infof("http server listening...")
					return server.ListenAndServe()
				}

				logger.Infof("https server listening...")
				return server.ListenAndServeTLS(cfg.TLSCertFilePath, cfg.TLSKeyFilePath)
			},
			func(_ error) {
				logger.Infof("start draining connections")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				err := server.Shutdown(ctx)
				if err != nil {
					logger.Errorf("error while shutting down the server: %s", err)
				} else {
					logger.Infof("server stopped")
				}
			},
		)
	}

	// Slurm HTTP server.
	{
		logger := logger.WithKV(log.KV{"addr": cfg.SlurmListenAddr, "http-server": "slurm"})

		// Slurm handler
		wh, err := slurm.New(slurm.Config{
			Logger: logger,
		}, configMapHandler, slurmHandler)
		if err != nil {
			return fmt.Errorf("could not create webhooks handler: %w", err)
		}

		mux := http.NewServeMux()
		mux.Handle("/", wh)
		server := http.Server{Addr: cfg.SlurmListenAddr, Handler: mux}

		g.Add(
			func() error {
				logger.Infof("http server listening...")
				return server.ListenAndServe()
			},
			func(_ error) {
				logger.Infof("start draining connections")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				err := server.Shutdown(ctx)
				if err != nil {
					logger.Errorf("error while shutting down the server: %s", err)
				} else {
					logger.Infof("server stopped")
				}

				err = sshHandler.Destruct()
				if err != nil {
					logger.Errorf("error while closing SSH connections: %s", err)
				}
			},
		)
	}

	// Watcher.
	{
		logger := logger.WithKV(log.KV{"service": "watcher"})

		// Watcher
		w, err := watcher.NewWatcher(configMapHandler, slurmHandler, logger)
		if err != nil {
			return fmt.Errorf("could not create watcher: %w", err)
		}

		ctx, cancel := context.WithCancel(context.Background())

		g.Add(
			func() error {
				logger.Infof("starting watcher...")
				return w.Watch(ctx)
			},
			func(_ error) {
				logger.Infof("stopping watcher")
				cancel()
			},
		)
	}

	err = g.Run()
	if err != nil {
		return err
	}

	return nil
}

func main() {
	err := runApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error running app: %s", err)
		os.Exit(1)
	}

	os.Exit(0)
}
