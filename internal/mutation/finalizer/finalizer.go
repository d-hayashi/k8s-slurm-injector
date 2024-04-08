package finalizer

import (
	"context"
	"fmt"

	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	"github.com/d-hayashi/k8s-slurm-injector/internal/slurm_handler"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Finalizer knows how to mark Kubernetes resources.
type Finalizer interface {
	Finalize(ctx context.Context, obj metav1.Object) (string, error)
}

// NewFinalizer returns a new finalizer that will finalize slurm jobs.
func NewFinalizer(
	configMapHandler config_map.ConfigMapHandler,
	slurmHandler slurm_handler.SlurmHandler,
	targetNamespace []string,
) (Finalizer, error) {
	return &finalizer{configMapHandler: configMapHandler, slurmHandler: slurmHandler, targetNamespace: targetNamespace}, nil
}

type finalizer struct {
	configMapHandler config_map.ConfigMapHandler
	slurmHandler     slurm_handler.SlurmHandler
	targetNamespace  []string
}

func (f finalizer) Finalize(_ context.Context, obj metav1.Object) (string, error) {
	var err error

	// Check if injection is enabled
	isInjectionEnabled := sidecar.IsInjectionEnabled(obj, f.targetNamespace, "")
	if !isInjectionEnabled {
		return "", nil
	}

	// Check if injected
	annotations := obj.GetAnnotations()
	status, exists := annotations["k8s-slurm-injector/status"]
	if status != "injected" || !exists {
		return "", nil
	}

	UUID, UUIDExists := annotations["k8s-slurm-injector/uuid"]
	if !UUIDExists {
		// Not a slurm job
		return "", nil
	}

	// Delete configmap
	cms, err := f.configMapHandler.ListConfigMaps("", metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=k8s-slurm-injector,k8s-slurm-injector/uuid=%s", UUID),
	})
	if err != nil {
		_ = fmt.Errorf("failed to list configmaps: %s", err)
	}
	for _, cm := range cms.Items {
		// Get jobid
		jobId, jobIdExists := cm.GetAnnotations()["k8s-slurm-injector/jobid"]
		if jobIdExists && jobId != "to-be-set" {
			_, err = f.slurmHandler.SCancel(jobId)
			if err != nil {
				_ = fmt.Errorf("failed to cancel job %s: %s", jobId, err)
			}
		}

		err = f.configMapHandler.DeleteConfigMap(cm.GetNamespace(), cm.GetName(), nil)
		if err != nil {
			_ = fmt.Errorf("failed to delete configmap %s: %s", cm.GetName(), err)
		}
	}

	return "Finalized the resource", err
}

// DummyMarker is a marker that doesn't do anything.
var DummyFinalizer Finalizer = dummyFinalizer(0)

type dummyFinalizer int

func (dummyFinalizer) Finalize(_ context.Context, _ metav1.Object) (string, error) { return "", nil }
