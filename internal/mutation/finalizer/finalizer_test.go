package finalizer_test

import (
	"context"
	"testing"

	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/slurm_handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/finalizer"
)

func TestFinalizer_Finalize(t *testing.T) {
	tests := map[string]struct {
		obj    metav1.Object
		expObj metav1.Object
	}{
		"Having a pod, the labels should be mutated.": {
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "enabled",
						"k8s-slurm-injector/node-specification-mode": "manual",
						"k8s-slurm-injector/node":                    "node1",
					},
					Annotations: map[string]string{
						"k8s-slurm-injector/namespace":               "default",
						"k8s-slurm-injector/object-name":             "pod-test",
						"k8s-slurm-injector/status":                  "injected",
						"k8s-slurm-injector/node-specification-mode": "manual",
						"k8s-slurm-injector/partition":               "",
						"k8s-slurm-injector/node":                    "node1",
						"k8s-slurm-injector/ntasks":                  "1",
						"k8s-slurm-injector/ncpus":                   "1",
						"k8s-slurm-injector/gpu-limit":               "false",
						"k8s-slurm-injector/gres":                    "",
						"k8s-slurm-injector/time":                    "1440",
						"k8s-slurm-injector/name":                    "",
					},
				},
			},
			expObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "enabled",
						"k8s-slurm-injector/node-specification-mode": "manual",
						"k8s-slurm-injector/node":                    "node1",
					},
					Annotations: map[string]string{
						"k8s-slurm-injector/namespace":               "default",
						"k8s-slurm-injector/object-name":             "pod-test",
						"k8s-slurm-injector/status":                  "injected",
						"k8s-slurm-injector/node-specification-mode": "manual",
						"k8s-slurm-injector/partition":               "",
						"k8s-slurm-injector/node":                    "node1",
						"k8s-slurm-injector/ntasks":                  "1",
						"k8s-slurm-injector/ncpus":                   "1",
						"k8s-slurm-injector/gpu-limit":               "false",
						"k8s-slurm-injector/gres":                    "",
						"k8s-slurm-injector/time":                    "1440",
						"k8s-slurm-injector/name":                    "",
					},
				},
			},
		},

		"Having a pod with labels without injection, the labels should not be mutated.": {
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "disabled",
						"k8s-slurm-injector/node-specification-mode": "manual",
						"k8s-slurm-injector/node":                    "node1",
					},
				},
			},
			expObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "disabled",
						"k8s-slurm-injector/node-specification-mode": "manual",
						"k8s-slurm-injector/node":                    "node1",
					},
				},
			},
		},

		"Having a pod without labels, the labels should not be mutated.": {
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Namespace: "default",
				},
			},
			expObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Namespace: "default",
				},
			},
		},

		"Having a service, the labels should not be mutated.": {
			obj: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Namespace: "default",
				},
			},
			expObj: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Namespace: "default",
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			dummyConfigMapHandler, _ := config_map.NewDummyConfigMapHandler()
			dummySlurmHandler, _ := slurm_handler.NewDummySlurmHandler()
			targetNamespace := []string{"target-.*"}
			handler, _ := finalizer.NewFinalizer(dummyConfigMapHandler, dummySlurmHandler, targetNamespace)

			_, err := handler.Finalize(context.TODO(), test.obj)
			require.NoError(err)

			switch test.obj.(type) {
			case *corev1.Pod:
				assert.Equal(test.expObj.(*corev1.Pod).ObjectMeta, test.obj.(*corev1.Pod).ObjectMeta)
				if len(test.obj.(*corev1.Pod).Spec.Containers) > 1 {
					assert.NotEmpty(test.obj.(*corev1.Pod).Spec.Containers[0].Lifecycle)
					assert.True(*test.obj.(*corev1.Pod).Spec.ShareProcessNamespace)
				}
			case *batchv1.Job:
				assert.Equal(test.expObj.(*batchv1.Job).ObjectMeta, test.obj.(*batchv1.Job).ObjectMeta)
			case *batchv1beta1.CronJob:
				assert.Equal(test.expObj.(*batchv1beta1.CronJob).ObjectMeta, test.obj.(*batchv1beta1.CronJob).ObjectMeta)
			default:
				assert.Equal(test.expObj, test.obj)
			}

		})
	}
}
