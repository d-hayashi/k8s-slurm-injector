package watcher

import (
	"context"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/log"
	"github.com/d-hayashi/k8s-slurm-injector/internal/slurm_handler"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// Get job-ids on kubernetes
	cms, err := w.configMapHandler.ListConfigMaps("", v1.ListOptions{
		LabelSelector: "app=k8s-slurm-injector",
	})
	if err != nil {
		return err
	}

	var jobIds []string
	for _, cm := range cms.Items {
		annotations := cm.GetAnnotations()
		if jobId, exists := annotations["k8s-slurm-injector/jobid"]; exists {
			jobIds = append(jobIds, jobId)
		}
	}

	w.state.jobIdsOnKubernetes = jobIds

	return nil
}

func (w watcher) deleteConfigMapOfJobId(jobId string) error {
	// Get job-ids on kubernetes
	cms, err := w.configMapHandler.ListConfigMaps("", v1.ListOptions{
		LabelSelector: fmt.Sprintf("app=k8s-slurm-injector,k8s-slurm-injector/jobid=%s", jobId),
	})
	if err != nil {
		return err
	}

	if len(cms.Items) == 0 {
		return fmt.Errorf("could not find a config-map which corresponds to job-id: %s", jobId)
	}

	for _, cm := range cms.Items {
		w.logger.Debugf("deleting config-map %s in namespace %s", cm.GetName(), cm.GetNamespace())
		err = w.configMapHandler.DeleteConfigMap(cm.GetNamespace(), cm.GetName(), nil)
		if err != nil {
			return err
		}
	}

	return nil
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
				}
				// Remove job-id from kill-candidates
				delete(w.state.killCandidates, jobIdOnSlurm)
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
			if err := w.deleteConfigMapOfJobId(jobIdOnKubernetes); err != nil {
				w.logger.Errorf("could not delete the corresponding config-map: %s", err.Error())
			}
		}
	}
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
