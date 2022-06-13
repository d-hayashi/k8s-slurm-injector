package webhook

import (
	"context"
	"fmt"
	"net/http"

	kwhhttp "github.com/slok/kubewebhook/v2/pkg/http"
	kwhlog "github.com/slok/kubewebhook/v2/pkg/log"
	kwhmodel "github.com/slok/kubewebhook/v2/pkg/model"
	kwhwebhook "github.com/slok/kubewebhook/v2/pkg/webhook"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/d-hayashi/k8s-slurm-injector/internal/log"
)

// kubewebhookLogger is a small proxy to use our logger with Kubewebhook.
type kubewebhookLogger struct {
	log.Logger
}

func (l kubewebhookLogger) WithValues(kv map[string]interface{}) kwhlog.Logger {
	return kubewebhookLogger{Logger: l.Logger.WithKV(kv)}
}
func (l kubewebhookLogger) WithCtxValues(ctx context.Context) kwhlog.Logger {
	return l.WithValues(kwhlog.ValuesFromCtx(ctx))
}
func (l kubewebhookLogger) SetValuesOnCtx(parent context.Context, values map[string]interface{}) context.Context {
	return kwhlog.CtxWithValues(parent, values)
}

// injectSidecar sets up the webhook handler for injecting sidecars for slurm using Kubewebhook library.
func (h handler) injectSidecar() (http.Handler, error) {
	mt := kwhmutating.MutatorFunc(func(ctx context.Context, ar *kwhmodel.AdmissionReview, obj metav1.Object) (*kwhmutating.MutatorResult, error) {
		msg, err := h.sidecar.Inject(ctx, obj)
		if err != nil {
			return nil, fmt.Errorf("could not inject sidecars with the resource: %w", err)
		}

		var warnings []string
		if msg != "" {
			warnings = []string{msg}
		}

		return &kwhmutating.MutatorResult{
			MutatedObject: obj,
			Warnings:      warnings,
		}, nil
	})

	logger := kubewebhookLogger{Logger: h.logger.WithKV(log.KV{"lib": "kubewebhook", "webhook": "slurmInjection"})}
	wh, err := kwhmutating.NewWebhook(kwhmutating.WebhookConfig{
		ID:      "slurmInjection",
		Logger:  logger,
		Mutator: mt,
	})
	if err != nil {
		return nil, fmt.Errorf("could not create webhook: %w", err)
	}
	whHandler, err := kwhhttp.HandlerFor(kwhhttp.HandlerConfig{
		Webhook: kwhwebhook.NewMeasuredWebhook(h.metrics, wh),
		Logger:  logger,
	})
	if err != nil {
		return nil, fmt.Errorf("could not create handler from webhook: %w", err)
	}

	return whHandler, nil
}

// initFinalizer sets up the webhook handler for finalizing slurm jobs.
func (h handler) initFinalizer() (http.Handler, error) {
	mt := kwhmutating.MutatorFunc(func(ctx context.Context, ar *kwhmodel.AdmissionReview, obj metav1.Object) (*kwhmutating.MutatorResult, error) {
		msg, err := h.finalizer.Finalize(ctx, obj)
		if err != nil {
			return nil, fmt.Errorf("could not finalize the resource: %w", err)
		}

		var warnings []string
		if msg != "" {
			warnings = []string{msg}
		}

		return &kwhmutating.MutatorResult{
			MutatedObject: obj,
			Warnings:      warnings,
		}, nil
	})

	logger := kubewebhookLogger{Logger: h.logger.WithKV(log.KV{"lib": "kubewebhook", "webhook": "slurmFinalizer"})}
	wh, err := kwhmutating.NewWebhook(kwhmutating.WebhookConfig{
		ID:      "slurmFinalizer",
		Logger:  logger,
		Mutator: mt,
	})
	if err != nil {
		return nil, fmt.Errorf("could not create webhook: %w", err)
	}
	whHandler, err := kwhhttp.HandlerFor(kwhhttp.HandlerConfig{
		Webhook: kwhwebhook.NewMeasuredWebhook(h.metrics, wh),
		Logger:  logger,
	})
	if err != nil {
		return nil, fmt.Errorf("could not create handler from webhook: %w", err)
	}

	return whHandler, nil
}
