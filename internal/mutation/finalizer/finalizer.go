package finalizer

import (
	"context"
	"github.com/d-hayashi/k8s-slurm-injector/internal/ssh_handler"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Finalizer knows how to mark Kubernetes resources.
type Finalizer interface {
	Finalize(ctx context.Context, obj metav1.Object) error
}

// NewFinalizer returns a new finalizer that will finalize slurm jobs.
func NewFinalizer(sshHandler ssh_handler.SSHHandler) (Finalizer, error) {
	return &finalizer{ssh: sshHandler}, nil
}

type finalizer struct {
	ssh        ssh_handler.SSHHandler
}

func (f finalizer) Finalize(_ context.Context, obj metav1.Object) error {
	var err error

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


	return err
}

// DummyMarker is a marker that doesn't do anything.
var DummyFinalizer Finalizer = dummyFinalizer(0)

type dummyFinalizer int

func (dummyFinalizer) Finalize(_ context.Context, _ metav1.Object) error { return nil }
