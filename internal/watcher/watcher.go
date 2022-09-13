package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/d-hayashi/k8s-slurm-injector/internal/client_set"
	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/log"
	"github.com/d-hayashi/k8s-slurm-injector/internal/slurm_handler"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type Watcher interface {
	Watch(ctx context.Context) error
}

type watcher struct {
	configMapHandler   config_map.ConfigMapHandler
	slurm              slurm_handler.SlurmHandler
	logger             log.Logger
	state              jobState
	TLSCertFilePath    string
	TLSKeyFilePath     string
	TLSCertFileContent string
	TLSKeyFileContent  string
}

type jobState struct {
	jobIdsOnSlurm      []string
	jobIdsOnKubernetes []string
	killCandidates     map[string]bool
}

type patchStringValue struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

type dummyWatcher struct{}

func NewWatcher(
	configMapHandler config_map.ConfigMapHandler,
	slurm slurm_handler.SlurmHandler,
	logger log.Logger,
	TLSCertFilePath string,
	TLSKeyFilePath string,
) (Watcher, error) {
	TLSCertFileContent := ""
	TLSKeyFileContent := ""

	if TLSCertFilePath != "" {
		TLSCertFileContent = readFile(TLSCertFilePath)
	}
	if TLSKeyFilePath != "" {
		TLSKeyFileContent = readFile(TLSKeyFilePath)
	}

	state := jobState{
		jobIdsOnSlurm:      []string{},
		jobIdsOnKubernetes: []string{},
		killCandidates:     map[string]bool{},
	}
	w := watcher{
		configMapHandler:   configMapHandler,
		slurm:              slurm,
		logger:             logger,
		state:              state,
		TLSCertFilePath:    TLSCertFilePath,
		TLSKeyFilePath:     TLSKeyFilePath,
		TLSCertFileContent: TLSCertFileContent,
		TLSKeyFileContent:  TLSKeyFileContent,
	}

	return &w, nil
}

func NewDummyWatcher() (Watcher, error) {
	w := dummyWatcher{}
	return &w, nil
}

func readFile(filePath string) string {
	bytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		panic(err)
	}
	return string(bytes)
}

func (w watcher) Watch(ctx context.Context) error {
	innerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for {
		select {
		case <-innerCtx.Done():
			w.logger.Infof("watch cancelled")
			return nil
		case <-time.After(10 * time.Second):
			w.routine()
			w.logger.Debugf("jobs checked")
		}
	}
}

func (w watcher) checkTLSCertFileContent() {
	currentContent := readFile(w.TLSCertFilePath)
	if currentContent != w.TLSCertFileContent {
		panic("TLSCertFile has been updated")
	}
}

func (w watcher) checkTLSKeyFileContent() {
	currentContent := readFile(w.TLSKeyFilePath)
	if currentContent != w.TLSKeyFileContent {
		panic("TLSKeyFile has been updated")
	}
}

func (w *watcher) fetchJobIdsOnSlurm() error {
	// Get job-ids on slurm
	jobIds, err := w.slurm.List()
	if err != nil {
		return err
	}

	w.state.jobIdsOnSlurm = jobIds
	return nil
}

func (w *watcher) fetchJobIdsOnKubernetes() error {
	// Initialize client-set
	clientset := client_set.GetClientSet()

	// Get job-ids on kubernetes
	cms, err := w.configMapHandler.ListConfigMaps("", metav1.ListOptions{
		LabelSelector: "app=k8s-slurm-injector",
	})
	if err != nil {
		return err
	}

	var jobIds []string
	for _, cm := range cms.Items {
		annotations := cm.GetAnnotations()
		if jobId, exists := annotations["k8s-slurm-injector/jobid"]; exists {
			// Make sure that the corresponding pod exists
			namespace, namespaceExists := annotations["k8s-slurm-injector/namespace"]
			objectName, objectNameExists := annotations["k8s-slurm-injector/object-name"]
			if namespaceExists && objectNameExists {
				if strings.HasPrefix(objectName, "pod-") {
					objs, _err := clientset.CoreV1().Pods(namespace).List(
						context.TODO(),
						metav1.ListOptions{LabelSelector: fmt.Sprintf("k8s-slurm-injector/object-name=%s", objectName)},
					)
					if _err != nil {
						w.logger.Debugf("err getting a pod with job-id '%s': %s", jobId, _err)
					}
					if len(objs.Items) > 0 || !errors.IsNotFound(_err) {
						jobIds = append(jobIds, jobId)
					}
				} else {
					jobIds = append(jobIds, jobId)
				}
			} else {
				jobIds = append(jobIds, jobId)
			}
		}
	}

	w.state.jobIdsOnKubernetes = jobIds

	return nil
}

func (w watcher) deleteKubernetesResourcesOfJobId(jobId string) error {
	// Get job-ids on kubernetes
	cms, err := w.configMapHandler.ListConfigMaps("", metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=k8s-slurm-injector,k8s-slurm-injector/jobid=%s", jobId),
	})
	if err != nil {
		return err
	}

	if len(cms.Items) == 0 {
		return fmt.Errorf("could not find a config-map which corresponds to job-id: %s", jobId)
	}

	// Initialize client-set
	clientset := client_set.GetClientSet()

	for _, cm := range cms.Items {
		// Get the corresponding object name and namespace
		objectNamespace := ""
		objectName := ""
		for key, value := range cm.GetAnnotations() {
			if key == "k8s-slurm-injector/namespace" {
				objectNamespace = value
			}
			if key == "k8s-slurm-injector/object-name" {
				objectName = value
			}
		}
		if objectNamespace == "" || objectName == "" {
			return fmt.Errorf("failed to get object information from config-map '%s'", cm.GetName())
		}

		// Delete the resources related to the config-map
		if strings.HasPrefix(objectName, "pod-") {
			// Check if the pod exist
			podName := strings.Replace(objectName, "pod-", "", 1)
			_, err := clientset.CoreV1().Pods(objectNamespace).Get(context.TODO(), podName, metav1.GetOptions{})
			if err == nil {
				w.logger.Debugf("deleting pod %s in namespace %s", podName, objectNamespace)
				err = clientset.CoreV1().Pods(objectNamespace).Delete(context.TODO(), podName, metav1.DeleteOptions{})
				if err != nil {
					return err
				}
			}
		} else {
			return fmt.Errorf("cannot delete object (unsupported): %s", objectName)
		}

		// Delete the config-map
		w.logger.Debugf("deleting config-map %s in namespace %s", cm.GetName(), cm.GetNamespace())
		err = w.configMapHandler.DeleteConfigMap(cm.GetNamespace(), cm.GetName(), nil)
		if err != nil {
			return err
		}
	}

	return nil
}

func (w *watcher) updateNodeLabels() {
	// Initialize client-set
	clientset := client_set.GetClientSet()

	// Get nodes
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return
	}

	// Get node-status on slurm
	nodeStatus := w.slurm.GetNodeStatus()
	for _, status := range nodeStatus {
		for _, node := range nodes.Items {
			if status.Node == node.Name {
				w.logger.Debugf("Updating labels of node %s", status.Node)
				payload := []patchStringValue{
					{
						Op:    "replace",
						Path:  "/metadata/labels/slurm-num-cpus-free",
						Value: status.NumCPUsFree,
					},
					{
						Op:    "replace",
						Path:  "/metadata/labels/slurm-num-gpus-free",
						Value: status.NumGPUsFree,
					},
					{
						Op:    "replace",
						Path:  "/metadata/labels/slurm-mem-free",
						Value: status.MemFree,
					},
				}
				payloadBytes, _ := json.Marshal(payload)
				_, err := clientset.CoreV1().Nodes().Patch(context.TODO(), node.Name, types.JSONPatchType, payloadBytes, metav1.PatchOptions{})
				if err != nil {
					w.logger.Errorf("failed to label node %s: %s", node.Name, err)
				}
			}
		}
	}
}

func (w *watcher) routine() {
	if w.TLSCertFilePath != "" {
		w.checkTLSCertFileContent()
	}

	if w.TLSKeyFilePath != "" {
		w.checkTLSKeyFileContent()
	}

	if err := w.fetchJobIdsOnSlurm(); err != nil {
		w.logger.Errorf("could not fetch job-ids on slurm: %s", err.Error())
		return
	}
	if err := w.fetchJobIdsOnKubernetes(); err != nil {
		w.logger.Errorf("could not fetch job-ids on kubernetes: %s", err.Error())
		return
	}

	for _, jobIdOnSlurm := range w.state.jobIdsOnSlurm {
		isExists := false
		for _, jobIdOnKubernetes := range w.state.jobIdsOnKubernetes {
			if jobIdOnSlurm == jobIdOnKubernetes {
				isExists = true
			}
		}
		if !isExists {
			// In case the job-id only exists on slurm, add to kill-candidates first.
			if _, exists := w.state.killCandidates[jobIdOnSlurm]; exists {
				w.logger.Infof("killing slurm job '%s'", jobIdOnSlurm)
				_, err := w.slurm.SCancel(jobIdOnSlurm)
				if err != nil {
					w.logger.Errorf("could not scancel job-id '%s': %s", jobIdOnSlurm, err.Error())
				} else {
					// Remove job-id from kill-candidates
					delete(w.state.killCandidates, jobIdOnSlurm)
				}
			} else {
				w.logger.Debugf("adding slurm job '%s' to kill-candidate", jobIdOnSlurm)
				w.state.killCandidates[jobIdOnSlurm] = true
			}
		}
	}

	for _, jobIdOnKubernetes := range w.state.jobIdsOnKubernetes {
		isExists := false
		for _, jobIdOnSlurm := range w.state.jobIdsOnSlurm {
			if jobIdOnKubernetes == jobIdOnSlurm {
				isExists = true
			}
		}
		if !isExists {
			// Remove the corresponding config-map in case that the job-id does not exist on Slurm
			w.logger.Infof("deleting config-map of job-id %s", jobIdOnKubernetes)
			if err := w.deleteKubernetesResourcesOfJobId(jobIdOnKubernetes); err != nil {
				w.logger.Errorf("could not delete the corresponding config-map: %s", err.Error())
			}
		}
	}

	w.updateNodeLabels()
}

func (w dummyWatcher) Watch(ctx context.Context) error {
	innerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for {
		select {
		case <-innerCtx.Done():
			print("cancel\n")
			return nil
		case <-time.After(1 * time.Second):
			// do nothing
		}
	}
}
