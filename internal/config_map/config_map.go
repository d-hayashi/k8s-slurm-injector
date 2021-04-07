package config_map

import (
	"context"
	"fmt"
	"regexp"

	"github.com/d-hayashi/k8s-slurm-injector/internal/client_set"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ConfigMapHandler interface {
	GetConfigMap(namespace string, name string) (*v1.ConfigMap, error)
	CreateConfigMap(namespace string, cm *v1.ConfigMap) (*v1.ConfigMap, error)
	UpdateConfigMap(namespace string, cm *v1.ConfigMap) (*v1.ConfigMap, error)
	DeleteConfigMap(namespace string, name string) error
}

type handler struct {
	clientset *kubernetes.Clientset
}

type dummyHandler struct{}

func NewConfigMapHandler() (ConfigMapHandler, error) {
	clientset := client_set.GetClientSet()
	handler := handler{clientset: clientset}
	return &handler, nil
}

func NewDummyConfigMapHandler() (ConfigMapHandler, error) {
	handler := dummyHandler{}
	return &handler, nil
}

func (c handler) GetConfigMap(namespace string, name string) (*v1.ConfigMap, error) {
	cm, err := c.clientset.CoreV1().ConfigMaps(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	return cm, err
}

func (c handler) CreateConfigMap(namespace string, cm *v1.ConfigMap) (*v1.ConfigMap, error) {
	configMap, err := c.clientset.CoreV1().ConfigMaps(namespace).Create(context.TODO(), cm, metav1.CreateOptions{})
	return configMap, err
}

func (c handler) UpdateConfigMap(namespace string, cm *v1.ConfigMap) (*v1.ConfigMap, error) {
	configMap, err := c.clientset.CoreV1().ConfigMaps(namespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
	return configMap, err
}

func (c handler) DeleteConfigMap(namespace string, name string) error {
	err := c.clientset.CoreV1().ConfigMaps(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
	return err
}

func (d dummyHandler) GetConfigMap(namespace string, name string) (*v1.ConfigMap, error) {
	cm := v1.ConfigMap{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				"k8s-slurm-injector/jobid": "1234567890",
			},
		},
		Immutable:  nil,
		Data:       nil,
		BinaryData: nil,
	}
	return &cm, nil
}

func (d dummyHandler) CreateConfigMap(_ string, cm *v1.ConfigMap) (*v1.ConfigMap, error) {
	return cm, nil
}

func (d dummyHandler) UpdateConfigMap(_ string, cm *v1.ConfigMap) (*v1.ConfigMap, error) {
	return cm, nil
}

func (d dummyHandler) DeleteConfigMap(_ string, _ string) error {
	return nil
}

func ConfigMapNameFromObjectName(objectName string) string {
	regString := regexp.MustCompile(`[^0-9A-Za-z_#:-]`)
	name := regString.ReplaceAllString(objectName, "")
	return fmt.Sprintf("k8s-slurm-injector-config-%s", name)
}
