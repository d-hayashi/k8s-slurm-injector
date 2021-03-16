package sidecar

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
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
	// Filter object
	switch obj.(type) {
	case *corev1.Pod:
		// pass
	case *batchv1.Job:
		// pass
	case *batchv1beta1.CronJob:
		// pass
	default:
		return nil
	}

	// Get labels
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	// Check labels
	isInjection := false
	for key, value := range labels {
		if key == "k8s-slurm-injector/injection" && value == "enabled" {
			isInjection = true
		}
	}
	if !isInjection {
		return nil
	}

	// Add init-container
	initContainer := corev1.Container{
		Name:            "initial-container",
		Image:           "busybox",
		Args:            []string{"sleep", "10"},
		ImagePullPolicy: "IfNotPresent",
	}

	switch v := obj.(type) {
	case *corev1.Pod:
		v.Spec.InitContainers = append(v.Spec.InitContainers, initContainer)
	case *batchv1.Job:
		v.Spec.Template.Spec.InitContainers = append(v.Spec.Template.Spec.InitContainers, initContainer)
	case *batchv1beta1.CronJob:
		v.Spec.JobTemplate.Spec.Template.Spec.InitContainers =
			append(v.Spec.JobTemplate.Spec.Template.Spec.InitContainers, initContainer)
	default:
		return fmt.Errorf("not implemented")
	}

	// Modify labels
	labels["k8s-slurm-injector/status"] = "injected"
	obj.SetLabels(labels)

	return nil
}

// DummyMarker is a marker that doesn't do anything.
var DummySidecarInjector SidecarInjector = dummySidecarInjector(0)

type dummySidecarInjector int

func (dummySidecarInjector) Inject(_ context.Context, _ metav1.Object) error { return nil }
