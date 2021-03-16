package sidecar_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
)

func TestLabelMarkerMark(t *testing.T) {
	tests := map[string]struct {
		obj    metav1.Object
		expObj metav1.Object
	}{
		"Having a pod, the labels should be mutated.": {
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "enabled",
						"k8s-slurm-injector/ssh-user":  "user",
						"k8s-slurm-injector/ssh-host":  "localhost",
					},
				},
			},
			expObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "enabled",
						"k8s-slurm-injector/ssh-user":  "user",
						"k8s-slurm-injector/ssh-host":  "localhost",
					},
					Annotations: map[string]string{
						"k8s-slurm-injector/status": "injected",
					},
				},
			},
		},

		"Having a pod with labels without injection, the labels should not be mutated.": {
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "disabled",
					},
				},
			},
			expObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "disabled",
					},
				},
			},
		},

		"Having a pod without labels, the labels should not be mutated.": {
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
			expObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
		},

		"Having a job, the labels should be mutated.": {
			obj: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "enabled",
						"k8s-slurm-injector/ssh-user":  "user",
						"k8s-slurm-injector/ssh-host":  "localhost",
					},
				},
			},
			expObj: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"test1":                        "value1",
						"test2":                        "value2",
						"k8s-slurm-injector/injection": "enabled",
						"k8s-slurm-injector/ssh-user":  "user",
						"k8s-slurm-injector/ssh-host":  "localhost",
					},
					Annotations: map[string]string{
						"k8s-slurm-injector/status": "injected",
					},
				},
			},
		},

		"Having a service, the labels should not be mutated.": {
			obj: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
			expObj: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			injector := sidecar.NewSidecarInjector()

			err := injector.Inject(context.TODO(), test.obj)
			require.NoError(err)

			switch test.obj.(type) {
			case *corev1.Pod:
				assert.Equal(test.expObj.(*corev1.Pod).ObjectMeta, test.obj.(*corev1.Pod).ObjectMeta)
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
