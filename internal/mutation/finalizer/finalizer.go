package finalizer

import (
	"context"
	"fmt"

	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	"github.com/d-hayashi/k8s-slurm-injector/internal/slurm_handler"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	var _objectname string

	// Filter object
	switch v := obj.(type) {
	case *corev1.Pod:
		_objectname = fmt.Sprintf("pod-%s", v.Name)
	case *batchv1.Job:
		_objectname = fmt.Sprintf("job-%s", v.Name)
	case *batchv1beta1.CronJob:
		_objectname = fmt.Sprintf("cronjob-%s", v.Name)
	default:
		return "", nil
	}

	// Check if injection is enabled
	isInjectionEnabled := sidecar.IsInjectionEnabled(obj, f.targetNamespace)
	if !isInjectionEnabled {
		return "", nil
	}

	// Check if injected
	annotations := obj.GetAnnotations()
	status, exists := annotations["k8s-slurm-injector/status"]
	if status != "injected" || !exists {
		return "", nil
	}

	namespace, exists := annotations["k8s-slurm-injector/namespace"]
	if !exists {
		namespace = obj.GetNamespace()
	}
	objectName, exists := annotations["k8s-slurm-injector/object-name"]
	if !exists {
		objectName = _objectname
	}
	configMapName := config_map.ConfigMapNameFromObjectName(objectName)

	if objectName != "" && namespace != "" {
		// Delete the corresponding config-map if exists
		configMap, err := f.configMapHandler.GetConfigMap(namespace, configMapName, nil)
		if errors.IsNotFound(err) {
			fmt.Printf("config-map '%s' does not exist, skipped deleting it", configMapName)
		} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
			fmt.Printf("error getting configmap %v", statusError.ErrStatus.Message)
		} else if err != nil {
			fmt.Printf("error getting configmap: %s", err.Error())
		} else {
			// Get jobid from config-map
			annotations = configMap.GetAnnotations()
			if jobid, exists := annotations["k8s-slurm-injector/jobid"]; exists {
				// Call scancel with the jobid
				_, err = f.slurmHandler.SCancel(jobid)
				if err != nil {
					// TODO: Retry
					_ = fmt.Errorf("%s", err.Error())
				}
			}

			// Delete the config-map
			err = f.configMapHandler.DeleteConfigMap(namespace, configMapName, nil)
			if err != nil {
				// TODO: Retry
				_ = fmt.Errorf("%s", err.Error())
			}
		}
	}

	return "Finalized the resource", err
}

// DummyMarker is a marker that doesn't do anything.
var DummyFinalizer Finalizer = dummyFinalizer(0)

type dummyFinalizer int

func (dummyFinalizer) Finalize(_ context.Context, _ metav1.Object) (string, error) { return "", nil }
