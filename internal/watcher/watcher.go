package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
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
	// Initialization
	clientset := client_set.GetClientSet()
	jobStatesDict := make(map[string]JobStateOnKubernetes)

	// Get pods on kubernetes
	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		LabelSelector: "k8s-slurm-injector/status=injected=injected",
	})
	if err != nil {
		return err
	}
	// Get configmaps on kubernetes
	cms, err := w.configMapHandler.ListConfigMaps("", metav1.ListOptions{
		LabelSelector: "app=k8s-slurm-injector",
	})
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		annotations := pod.GetAnnotations()
		if UUID, uuidExists := annotations["k8s-slurm-injector/uuid"]; uuidExists {
			if jobId, jobIdExists := annotations["k8s-slurm-injector/jobid"]; jobIdExists {
				configMapExists := false
				for _, cm := range cms.Items {
					if cmUUID, cmUUIDExists := cm.GetAnnotations()["k8s-slurm-injector/uuid"]; cmUUIDExists {
						if cmUUID == UUID {
							configMapExists = true
						}
					}
				}
				if configMapExists {
					jobStateOnKubernetes := JobStateOnKubernetes{UUID: UUID, jobId: jobId}
					jobStatesDict[UUID] = jobStateOnKubernetes
				} else {
					// Pod exists but configmap does not exist
					w.logger.Infof("deleting pod %s in namespace %s", pod.GetName(), pod.GetNamespace())
					err = clientset.CoreV1().Pods(pod.GetNamespace()).Delete(context.TODO(), pod.GetName(), metav1.DeleteOptions{})
					if err != nil {
						w.logger.Errorf("err deleting a pod with UUID '%s': %s", UUID, err)
					}
				}
			}
		}
	}

	for _, cm := range cms.Items {
		annotations := cm.GetAnnotations()
		if jobId, jobIdExists := annotations["k8s-slurm-injector/jobid"]; jobIdExists {
			if UUID, uuidExists := annotations["k8s-slurm-injector/uuid"]; uuidExists {
				podExists := false
				for _, pod := range pods.Items {
					if podUUID, podUUIDExists := pod.GetAnnotations()["k8s-slurm-injector/uuid"]; podUUIDExists {
						if podUUID == UUID {
							podExists = true
						}
					}
				}
				if podExists {
					jobStateOnKubernetes := JobStateOnKubernetes{UUID: UUID, jobId: jobId}
					jobStatesDict[UUID] = jobStateOnKubernetes
				} else {
					// Configmap exists but pod does not exist
					if jobId == "to-be-set" {
						w.logger.Debugf("Job with UUID %s is being initialized", UUID)
						jobStateOnKubernetes := JobStateOnKubernetes{UUID: UUID, jobId: jobId}
						jobStatesDict[UUID] = jobStateOnKubernetes
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

	// Convert map to slice
	var jobStatesOnKubernetes []JobStateOnKubernetes
	for _, jobStateOnKubernetes := range jobStatesDict {
		jobStatesOnKubernetes = append(jobStatesOnKubernetes, jobStateOnKubernetes)
	}

	w.state.jobStatesOnKubernetes = jobStatesOnKubernetes

	return nil
}

func (w watcher) deleteKubernetesResourcesOfUUID(UUID string) error {
	// Initialize client-set
	clientset := client_set.GetClientSet()

	// Get pod
	pods, err := clientset.CoreV1().Pods("").List(
		context.TODO(),
		metav1.ListOptions{LabelSelector: fmt.Sprintf("k8s-slurm-injector/uuid=%s", UUID)},
	)
	if err != nil {
		return err
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("could not find a pod which corresponds to UUID: %s", UUID)
	}

	// Delete pod
	for _, pod := range pods.Items {
		err = clientset.CoreV1().Pods(pod.GetNamespace()).Delete(context.TODO(), pod.GetName(), metav1.DeleteOptions{})
		if err != nil {
			return err
		}
	}

	// Get configmap
	cms, err := w.configMapHandler.ListConfigMaps("", metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=k8s-slurm-injector,k8s-slurm-injector/uuid=%s", UUID),
	})
	if err != nil {
		return err
	}

	if len(cms.Items) == 0 {
		return fmt.Errorf("could not find a config-map which corresponds to UUID: %s", UUID)
	}

	// Delete config-map
	for _, cm := range cms.Items {
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

	w.logger.Debugf("jobStatesOnSlurm: %v", w.state.jobStatesOnSlurm)
	w.logger.Debugf("jobStatesOnKubernetes: %v", w.state.jobStatesOnKubernetes)

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
			// Remove the corresponding kubernetes resources if the job only exists on kubernetes
			w.logger.Infof("deleting pod and config-map of UUID %s", jobStateOnKubernetes.UUID)
			if err := w.deleteKubernetesResourcesOfUUID(jobStateOnKubernetes.UUID); err != nil {
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
