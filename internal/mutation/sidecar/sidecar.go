package sidecar

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SidecarInjector knows how to mark Kubernetes resources.
type SidecarInjector interface {
	Inject(ctx context.Context, obj metav1.Object) error
}

// NewSidecarInjector returns a new sidecar-injector that will inject sidecars.
func NewSidecarInjector() SidecarInjector {
	return sidecarinjector{}
}

type sidecarinjector struct{}

func (s sidecarinjector) Inject(_ context.Context, obj metav1.Object) error {
	// Missing generics...
	switch obj.(type) {
	case *corev1.Pod:
		// pass
	default:
		return nil
	}

	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels["k8s-slurm-injector/status"] = "injected"

	obj.SetLabels(labels)
	return nil
}

// DummyMarker is a marker that doesn't do anything.
var DummySidecarInjector SidecarInjector = dummySidecarInjector(0)

type dummySidecarInjector int

func (dummySidecarInjector) Inject(_ context.Context, _ metav1.Object) error { return nil }
