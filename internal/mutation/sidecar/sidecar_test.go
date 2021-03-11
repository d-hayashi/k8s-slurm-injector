package sidecar_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
						"test1": "value1",
						"test2": "value2",
					},
				},
			},
			expObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"test1":                     "value1",
						"test2":                     "value2",
						"k8s-slurm-injector/status": "injected",
					},
				},
			},
		},

		"Having a service, the labels should be mutated.": {
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

			assert.Equal(test.expObj, test.obj)
		})
	}
}
