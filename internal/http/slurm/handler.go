package slurm

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

type IsInjectableHandler struct {
	handler handler
}

type SbatchHandler struct {
	handler handler
}

type JobEnvHandler struct {
	handler handler
}

type JobEnvToConfigMapHandler struct {
	JobEnvHandler
}

type JobStateHandler struct {
	handler handler
}

type ScancelHandler struct {
	handler handler
}

func parseQueryParams(r *http.Request, jobInfo *sidecar.JobInformation) error {
	regString := regexp.MustCompile(`[^0-9A-Za-z_#:-]`)
	regDecimal := regexp.MustCompile(`[^0-9]`)
	jobInfo.Namespace = regString.ReplaceAllString(r.URL.Query().Get("namespace"), "")
	jobInfo.ObjectName = regString.ReplaceAllString(r.URL.Query().Get("objectname"), "")
	jobInfo.NodeSpecificationMode = regString.ReplaceAllString(r.URL.Query().Get("nodespecificationmode"), "")
	jobInfo.Partition = regString.ReplaceAllString(r.URL.Query().Get("partition"), "")
	jobInfo.Node = regString.ReplaceAllString(r.URL.Query().Get("node"), "")
	jobInfo.Ntasks = regDecimal.ReplaceAllString(r.URL.Query().Get("ntasks"), "")
	jobInfo.Ncpus = regDecimal.ReplaceAllString(r.URL.Query().Get("ncpus"), "")
	jobInfo.Ngpus = regDecimal.ReplaceAllString(r.URL.Query().Get("ngpus"), "")
	jobInfo.Gres = regString.ReplaceAllString(r.URL.Query().Get("gres"), "")
	jobInfo.Time = regDecimal.ReplaceAllString(r.URL.Query().Get("time"), "")
	jobInfo.Name = regString.ReplaceAllString(r.URL.Query().Get("name"), "")
	jobInfo.UUID = regString.ReplaceAllString(r.URL.Query().Get("uuid"), "")

	return nil
}

func (s SbatchHandler) prepareParams(jobInfo *sidecar.JobInformation) error {
	// Automatically select partition
	if jobInfo.NodeSpecificationMode != "manual" && jobInfo.Partition == "" {
		for _, nodeInfo := range s.handler.slurmHandler.GetNodeInfo() {
			if nodeInfo.Node == jobInfo.Node {
				jobInfo.Partition = nodeInfo.Partition
			}
		}
		if jobInfo.Partition == "" {
			return fmt.Errorf("unrecognized node: %s", jobInfo.Node)
		}
	}

	// Check nodes
	isNodeExists := false
	for _, nodeInfo := range s.handler.slurmHandler.GetNodeInfo() {
		if nodeInfo.Node == jobInfo.Node {
			isNodeExists = true
		}
	}
	if !isNodeExists {
		return fmt.Errorf("node %s does not exist", jobInfo.Node)
	}

	// Check partitions
	isPartitionExists := false
	for _, nodeInfo := range s.handler.slurmHandler.GetNodeInfo() {
		if nodeInfo.Partition == jobInfo.Partition {
			isPartitionExists = true
		}
	}
	if !isPartitionExists {
		return fmt.Errorf("partition %s does not exist", jobInfo.Partition)
	}

	return nil
}

func (s IsInjectableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.logger.Infof("isInjectable")

	jobInfo := sidecar.NewJobInformation()
	isInjectable := true

	// Parse request and check if the node is Slurm injectable
	err := parseQueryParams(r, jobInfo)
	if err == nil {
		// Check nodes
		isNodeExists := false
		for _, nodeInfo := range s.handler.slurmHandler.GetNodeInfo() {
			if nodeInfo.Node == jobInfo.Node {
				isNodeExists = true
			}
		}
		if !isNodeExists {
			isInjectable = false
		}
	}

	// Write to respond
	if err == nil {
		if isInjectable {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = fmt.Fprintf(w, "slurm job is not injectable as node %s does not exist", jobInfo.Node)
		}
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("failed to check if slurm job is injectable: %s", err.Error())
	}
}

func (h handler) isInjectable() (http.Handler, error) {
	return IsInjectableHandler{h}, nil
}

func (s SbatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.logger.Infof("sbatch")

	jobInfo := sidecar.NewJobInformation()

	// Parse request and construct commands
	err := parseQueryParams(r, jobInfo)
	if err == nil {
		err = s.prepareParams(jobInfo)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.handler.logger.Errorf("failed to sbatch: %s", err.Error())
			return
		}
	}

	// Create pre-sbatch configmap
	namespace := jobInfo.Namespace
	configMapName := config_map.ConfigMapNameFromObjectName(jobInfo.ObjectName)
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":                      "k8s-slurm-injector",
				"k8s-slurm-injector/jobid": "to-be-set",
				"k8s-slurm-injector/uuid":  jobInfo.UUID,
			},
			Annotations: map[string]string{
				"k8s-slurm-injector/last-applied-command": "sbatch",
				"k8s-slurm-injector/jobid":                "to-be-set",
				"k8s-slurm-injector/namespace":            namespace,
				"k8s-slurm-injector/object-name":          jobInfo.ObjectName,
				"k8s-slurm-injector/uuid":                 jobInfo.UUID,
			},
		},
	}

	_, err = s.handler.configMapHandler.GetConfigMap(namespace, configMapName, nil)
	if errors.IsNotFound(err) {
		// Create a config-map
		_, err := s.handler.configMapHandler.CreateConfigMap(namespace, cm, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.handler.logger.Errorf("error updating configmap '%s': %s", configMapName, err.Error())
			return
		}
		s.handler.logger.Infof("created config-map '%s'", configMapName)
	} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("error getting configmap %v", statusError.ErrStatus.Message)
		return
	} else if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("error getting configmap: %s", err.Error())
		return
	} else {
		// Update config-map
		s.handler.logger.Infof("updating config-map: %s", configMapName)
		_, err = s.handler.configMapHandler.UpdateConfigMap(namespace, cm, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.handler.logger.Errorf("error updating configmap '%s': %s", configMapName, err.Error())
			return
		}
		s.handler.logger.Infof("updated config-map '%s'", configMapName)
	}

	// Sbatch
	out, err := s.handler.slurmHandler.SBatch(jobInfo)

	// Write to respond
	_, _ = fmt.Fprint(w, out)

	if err == nil {
		// Update the configmap with the jobid
		cm, err := s.handler.configMapHandler.GetConfigMap(namespace, configMapName, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.handler.logger.Errorf("error getting configmap '%s': %s", configMapName, err.Error())
			return
		}
		labels := cm.GetLabels()
		labels["k8s-slurm-injector/jobid"] = out
		cm.SetLabels(labels)
		annotations := cm.GetAnnotations()
		annotations["k8s-slurm-injector/jobid"] = out
		cm.SetAnnotations(annotations)
		_, err = s.handler.configMapHandler.UpdateConfigMap(namespace, cm, nil)

	} else {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("failed to sbatch: %s (%s)", err.Error(), out)

		// Delete the configmap
		err = s.handler.configMapHandler.DeleteConfigMap(namespace, configMapName, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.handler.logger.Errorf("error deleting configmap '%s': %s", configMapName, err.Error())
		}
	}
}

func (h handler) sbatch() (http.Handler, error) {
	return SbatchHandler{h}, nil
}

func (s JobEnvHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	regDecimal := regexp.MustCompile(`[^0-9]`)
	jobid := regDecimal.ReplaceAllString(r.URL.Query().Get("jobid"), "")
	s.handler.logger.Infof("env jobid=%s", jobid)

	if jobid == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.handler.logger.Errorf("jobid is not given")
		return
	}

	out, err := s.handler.slurmHandler.GetEnv(jobid)

	// Write to respond
	if err == nil {
		_, _ = fmt.Fprint(w, out)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("failed to get envrionment variables of job: %s", err.Error())
	}
}

func (h handler) jobEnv() (http.Handler, error) {
	return JobEnvHandler{h}, nil
}

func (s JobEnvToConfigMapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	regString := regexp.MustCompile(`[^0-9A-Za-z_#:-]`)
	regDecimal := regexp.MustCompile(`[^0-9]`)
	jobid := regDecimal.ReplaceAllString(r.URL.Query().Get("jobid"), "")
	namespace := regString.ReplaceAllString(r.URL.Query().Get("namespace"), "")
	objectName := regString.ReplaceAllString(r.URL.Query().Get("objectname"), "")
	configMapName := config_map.ConfigMapNameFromObjectName(objectName)
	s.handler.logger.Infof("envToConfigMap jobid=%s, configmap=%s, namespace=%s", jobid, configMapName, namespace)

	if jobid == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.handler.logger.Errorf("jobid is not given")
		return
	}
	if objectName == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.handler.logger.Errorf("objectname is not given")
		return
	}
	if namespace == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.handler.logger.Errorf("namespace is not given")
		return
	}

	out, err := s.handler.slurmHandler.GetEnv(jobid)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("error getting environment variables: %s", out)
		return
	}

	// Create env data
	env := map[string]string{}
	isUse := false
	for _, variable := range strings.Split(out, "\n") {
		isUse = false
		keyvalue := strings.Split(variable, "=")
		if len(keyvalue) != 2 {
			continue
		}
		key := keyvalue[0]
		value := keyvalue[1]

		// Filter
		if strings.HasPrefix(key, "SLURM_") {
			isUse = true
		}
		if strings.HasPrefix(key, "CUDA_") {
			isUse = true
		}

		// Substitute
		if isUse {
			if key == "CUDA_VISIBLE_DEVICES" {
				env["NVIDIA_VISIBLE_DEVICES"] = value
			} else {
				env[key] = value
			}
		}
	}

	// Get config-map if exists
	cm, err := s.handler.configMapHandler.GetConfigMap(namespace, configMapName, nil)
	if errors.IsNotFound(err) {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("error updating configmap '%s': %s", configMapName, err.Error())
		return
	} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("error getting configmap %v", statusError.ErrStatus.Message)
		return
	} else if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("error getting configmap: %s", err.Error())
		return
	} else {
		// Update config-map
		s.handler.logger.Infof("updating config-map: %s", configMapName)
		bytes, err := json.Marshal(cm)
		if err != nil {
			s.handler.logger.Warningf("failed to jsonify previous-configuration")
			bytes = []byte{}
		}
		labels := cm.GetLabels()
		labels["k8s-slurm-injector/jobid"] = jobid
		cm.SetLabels(labels)
		annotations := cm.GetAnnotations()
		annotations["k8s-slurm-injector/jobid"] = jobid
		annotations["k8s-slurm-injector/last-applied-configuration"] = string(bytes)
		cm.SetAnnotations(annotations)
		cm.Data = env
		_, err = s.handler.configMapHandler.UpdateConfigMap(namespace, cm, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.handler.logger.Errorf("error updating configmap '%s': %s", configMapName, err.Error())
			return
		}
	}
}

func (h handler) jobEnvToConfigMap() (http.Handler, error) {
	return JobEnvToConfigMapHandler{JobEnvHandler{h}}, nil
}

func (s JobStateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	regDecimal := regexp.MustCompile(`[^0-9]`)
	jobid := regDecimal.ReplaceAllString(r.URL.Query().Get("jobid"), "")
	// s.handler.logger.Debugf("state jobid=%s", jobid)

	if jobid == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.handler.logger.Errorf("jobid is not given")
		return
	}

	state, err := s.handler.slurmHandler.State(jobid)

	// Write to respond
	if err == nil {
		_, _ = fmt.Fprint(w, state)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("failed to get job information: %s", err.Error())
	}
}

func (h handler) jobState() (http.Handler, error) {
	return JobStateHandler{h}, nil
}

func (s ScancelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	regDecimal := regexp.MustCompile(`[^0-9]`)
	jobid := regDecimal.ReplaceAllString(r.URL.Query().Get("jobid"), "")
	s.handler.logger.Infof("scancel jobid=%s", jobid)

	if jobid == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.handler.logger.Errorf("jobid is not given")
		return
	}

	out, err := s.handler.slurmHandler.SCancel(jobid)

	// Write to respond
	if err == nil {
		_, _ = fmt.Fprint(w, out)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("failed to scancel: %s", err.Error())
	}
}

func (h handler) scancel() (http.Handler, error) {
	return ScancelHandler{h}, nil
}
