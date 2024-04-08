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

type JobStateOnSlurm struct {
	UUID  string
	jobId string
}

type JobStateOnKubernetes struct {
	UUID  string
	jobId string
}

type jobState struct {
	jobStatesOnSlurm      []JobStateOnSlurm
	jobStatesOnKubernetes []JobStateOnKubernetes
	killCandidates        map[string]bool
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
		jobStatesOnSlurm:      []JobStateOnSlurm{},
		jobStatesOnKubernetes: []JobStateOnKubernetes{},
		killCandidates:        map[string]bool{},
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

func (w *watcher) fetchJobStateOnSlurm() error {
	// Get job-ids on slurm
	UUIDs, JobIDs, err := w.slurm.ListUUIDsAndJobIDs()
	if err != nil {
		return err
	}

	var jobStateOnSlurm []JobStateOnSlurm
	for i, UUID := range UUIDs {
		jobStateOnSlurm = append(jobStateOnSlurm, JobStateOnSlurm{UUID: UUID, jobId: JobIDs[i]})
	}
	w.state.jobStatesOnSlurm = jobStateOnSlurm
	return nil
}

func (w *watcher) fetchJobStateOnKubernetes() error {
	// Initialize client-set
	clientset := client_set.GetClientSet()

	// Get job-ids on kubernetes
	cms, err := w.configMapHandler.ListConfigMaps("", metav1.ListOptions{
		LabelSelector: "app=k8s-slurm-injector",
	})
	if err != nil {
		return err
	}

	var jobStatesOnKubernetes []JobStateOnKubernetes
	for _, cm := range cms.Items {
		annotations := cm.GetAnnotations()
		if jobId, jobIdExists := annotations["k8s-slurm-injector/jobid"]; jobIdExists {
			if UUID, uuidExists := annotations["k8s-slurm-injector/uuid"]; uuidExists {
				namespace, namespaceExists := annotations["k8s-slurm-injector/namespace"]
				if namespaceExists {
					objs, _err := clientset.CoreV1().Pods(namespace).List(
						context.TODO(),
						metav1.ListOptions{LabelSelector: fmt.Sprintf("k8s-slurm-injector/uuid=%s", UUID)},
					)
					if _err != nil {
						w.logger.Errorf("err getting a pod with UUID '%s': %s", UUID, _err)
						w.logger.Infof("deleting config-map %s in namespace %s", cm.GetName(), cm.GetNamespace())
						err = w.configMapHandler.DeleteConfigMap(cm.GetNamespace(), cm.GetName(), nil)
						if err != nil {
							w.logger.Errorf("err deleting a config-map with UUID '%s': %s", UUID, err)
						}
					} else {
						if len(objs.Items) > 0 {
							// Configmap and pod exist
							w.logger.Debugf("Job with UUID %s is running", UUID)
							jobStateOnKubernetes := JobStateOnKubernetes{UUID: UUID, jobId: jobId}
							jobStatesOnKubernetes = append(jobStatesOnKubernetes, jobStateOnKubernetes)
						} else {
							// Configmap exists but pod does not exist
							if jobId == "to-be-set" {
								w.logger.Debugf("Job with UUID %s is being initialized", UUID)
								jobStateOnKubernetes := JobStateOnKubernetes{UUID: UUID, jobId: jobId}
								jobStatesOnKubernetes = append(jobStatesOnKubernetes, jobStateOnKubernetes)
							} else {
								// if JobID is set but pod does not exist, it means the job is finished
								w.logger.Infof("deleting config-map %s in namespace %s", cm.GetName(), cm.GetNamespace())
								err = w.configMapHandler.DeleteConfigMap(cm.GetNamespace(), cm.GetName(), nil)
								if err != nil {
									w.logger.Errorf("err deleting a config-map with UUID '%s': %s", UUID, err)
								}
							}
						}
					}
				}
			}
		}
	}

	w.state.jobStatesOnKubernetes = jobStatesOnKubernetes

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
			pod, err := clientset.CoreV1().Pods(objectNamespace).Get(context.TODO(), podName, metav1.GetOptions{})
			if err == nil {
				// Check the condition of the pod
				isReady := "Unknown"
				for _, condition := range pod.Status.Conditions {
					if condition.Type == "Ready" {
						isReady = string(condition.Status)
					}
				}
				// If the pod is running with status "Ready", delete the pod
				if isReady == "True" && pod.Status.Phase == "Running" {
					w.logger.Debugf("deleting pod %s in namespace %s", podName, objectNamespace)
					err = clientset.CoreV1().Pods(objectNamespace).Delete(context.TODO(), podName, metav1.DeleteOptions{})
					if err != nil {
						return err
					}
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

	if err := w.fetchJobStateOnSlurm(); err != nil {
		w.logger.Errorf("could not fetch job-ids on slurm: %s", err.Error())
		return
	}
	if err := w.fetchJobStateOnKubernetes(); err != nil {
		w.logger.Errorf("could not fetch job-ids on kubernetes: %s", err.Error())
		return
	}

	for _, jobStateOnSlurm := range w.state.jobStatesOnSlurm {
		isExists := false
		for _, jobStateOnKubernetes := range w.state.jobStatesOnKubernetes {
			if jobStateOnSlurm.UUID == jobStateOnKubernetes.UUID {
				isExists = true
			}
		}
		if !isExists {
			// In case the job only exists on slurm, delete the corresponding job on slurm
			w.logger.Infof("killing slurm job '%s'", jobStateOnSlurm.jobId)
			_, err := w.slurm.SCancel(jobStateOnSlurm.jobId)
			if err != nil {
				w.logger.Errorf("could not scancel job-id '%s': %s", jobStateOnSlurm.jobId, err.Error())
			}
		}
	}

	for _, jobStateOnKubernetes := range w.state.jobStatesOnKubernetes {
		isExists := false
		for _, jobStateOnSlurm := range w.state.jobStatesOnSlurm {
			if jobStateOnKubernetes.UUID == jobStateOnSlurm.UUID {
				isExists = true
			}
		}
		if !isExists && jobStateOnKubernetes.jobId != "to-be-set" {
			// Remove the corresponding config-map in case that the job-id does not exist on Slurm
			w.logger.Infof("deleting config-map of job-id %s", jobStateOnKubernetes.jobId)
			if err := w.deleteKubernetesResourcesOfJobId(jobStateOnKubernetes.jobId); err != nil {
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
