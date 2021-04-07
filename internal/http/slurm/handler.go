package slurm

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	"github.com/d-hayashi/k8s-slurm-injector/internal/ssh_handler"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func ConfigMapNameFromObjectName(objectName string) string {
	regString := regexp.MustCompile(`[^0-9A-Za-z_#:-]`)
	name := regString.ReplaceAllString(objectName, "")
	return fmt.Sprintf("k8s-slurm-injector-config-%s", name)
}

func (s SbatchHandler) parseQueryParams(r *http.Request, jobInfo *sidecar.JobInformation) error {
	regString := regexp.MustCompile(`[^0-9A-Za-z_#:-]`)
	regDecimal := regexp.MustCompile(`[^0-9]`)
	jobInfo.NodeSpecificationMode = regString.ReplaceAllString(r.URL.Query().Get("nodespecificationmode"), "")
	jobInfo.Partition = regString.ReplaceAllString(r.URL.Query().Get("partition"), "")
	jobInfo.Node = regString.ReplaceAllString(r.URL.Query().Get("node"), "")
	jobInfo.Ntasks = regDecimal.ReplaceAllString(r.URL.Query().Get("ntasks"), "")
	jobInfo.Ncpus = regDecimal.ReplaceAllString(r.URL.Query().Get("ncpus"), "")
	jobInfo.Gres = regString.ReplaceAllString(r.URL.Query().Get("gres"), "")
	jobInfo.Time = regDecimal.ReplaceAllString(r.URL.Query().Get("time"), "")
	jobInfo.Name = regString.ReplaceAllString(r.URL.Query().Get("name"), "")

	return nil
}

func (s SbatchHandler) prepareParams(jobInfo *sidecar.JobInformation) error {
	// Automatically select partition
	if jobInfo.NodeSpecificationMode != "manual" && jobInfo.Partition == "" {
		for _, nodeInfo := range s.handler.nodeInfo {
			if nodeInfo.node == jobInfo.Node {
				jobInfo.Partition = nodeInfo.partition
			}
		}
		if jobInfo.Partition == "" {
			return fmt.Errorf("unrecognized node: %s", jobInfo.Node)
		}
	}

	// Check nodes
	isNodeExists := false
	for _, nodeInfo := range s.handler.nodeInfo {
		if nodeInfo.node == jobInfo.Node {
			isNodeExists = true
		}
	}
	if !isNodeExists {
		return fmt.Errorf("node %s does not exist", jobInfo.Node)
	}

	// Check partitions
	isPartitionExists := false
	for _, nodeInfo := range s.handler.nodeInfo {
		if nodeInfo.partition == jobInfo.Partition {
			isPartitionExists = true
		}
	}
	if !isPartitionExists {
		return fmt.Errorf("partition %s does not exist", jobInfo.Partition)
	}

	return nil
}

func (s SbatchHandler) constructCommand(jobInfo *sidecar.JobInformation, commands *[]string) error {
	jobName := jobInfo.Name
	if jobName == "" {
		jobName = "k8s-slurm-injector-job"
	}

	*commands = []string{
		"sbatch",
		"--parsable",
		"--output=/dev/null",
		"--error=/dev/null",
		fmt.Sprintf("--job-name=%s", jobName),
	}
	if jobInfo.Partition != "" {
		*commands = append(*commands, fmt.Sprintf("--partition=%s", jobInfo.Partition))
	}
	if jobInfo.Node != "" {
		*commands = append(*commands, fmt.Sprintf("--nodelist=%s", jobInfo.Node))
	}
	if jobInfo.Ntasks != "" {
		*commands = append(*commands, fmt.Sprintf("--ntasks=%s", jobInfo.Ntasks))
	}
	if jobInfo.Ncpus != "" {
		*commands = append(*commands, fmt.Sprintf("--cpus-per-task=%s", jobInfo.Ncpus))
	}
	if jobInfo.Gres != "" {
		*commands = append(*commands, fmt.Sprintf("--gres=%s", jobInfo.Gres))
	}
	if jobInfo.Time != "" {
		*commands = append(*commands, fmt.Sprintf("--time=%s", jobInfo.Time))
	}

	return nil
}

func (s SbatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.logger.Infof("sbatch")

	var out []byte
	var err error = nil
	var commands []string
	jobInfo := sidecar.NewJobInformation()

	// Parse request and construct commands
	err = s.parseQueryParams(r, jobInfo)
	if err == nil {
		err = s.prepareParams(jobInfo)
	}
	if err == nil {
		err = s.constructCommand(jobInfo, &commands)
	}

	// Execute ssh commands
	if err == nil {
		command := ssh_handler.SSHCommand{
			Command: strings.Join(commands, " "),
			StdinPipe: "#!/bin/sh " +
				"\n trap 'echo \"Killing...\"' SIGHUP SIGINT SIGQUIT SIGTERM " +
				"\n echo \"Sleeping.  Pid=$$\" " +
				"\n while true; do sleep 10 & wait $!; done",
		}
		out, err = s.handler.ssh.RunCommandCombined(command)
	}

	// Write to respond
	_, _ = fmt.Fprint(w, string(out))

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("failed to sbatch: %s (%s)", err.Error(), out)
	}
}

func (h handler) sbatch() (http.Handler, error) {
	return SbatchHandler{h}, nil
}

func getEnv(jobid string, s *JobEnvHandler) (string, error) {
	command := ssh_handler.SSHCommand{
		Command: fmt.Sprintf("srun --jobid=%s env", jobid),
	}
	out, err := s.handler.ssh.RunCommand(command)
	return string(out), err
}

func (s JobEnvHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	jobid := r.URL.Query().Get("jobid")
	s.handler.logger.Infof("env jobid=%s", jobid)

	if jobid == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.handler.logger.Errorf("jobid is not given")
		return
	}

	out, err := getEnv(jobid, &s)

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
	jobid := r.URL.Query().Get("jobid")
	namespace := r.URL.Query().Get("namespace")
	objectName := r.URL.Query().Get("objectname")
	configMapName := ConfigMapNameFromObjectName(objectName)
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

	out, err := getEnv(jobid, &s.JobEnvHandler)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("failed to get envrionment variables of job: %s", err.Error())
		return
	}

	// Get config-map if exists
	cm, err := s.handler.clientset.CoreV1().ConfigMaps(namespace).Get(context.TODO(), configMapName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		// Create a config-map
		s.handler.logger.Infof("creating a new config-map: %s", configMapName)
		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: namespace,
				Labels:    nil,
				Annotations: map[string]string{
					"k8s-slurm-injector/jobid": jobid,
				},
			},
			Immutable: nil,
			Data: map[string]string{
				"env": out,
			},
			BinaryData: nil,
		}
		_, err := s.handler.clientset.CoreV1().ConfigMaps(namespace).Create(context.TODO(), cm, metav1.CreateOptions{})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.handler.logger.Errorf("error updating configmap '%s': %s", configMapName, err.Error())
			return
		}
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
		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: namespace,
				Labels:    nil,
				Annotations: map[string]string{
					"k8s-slurm-injector/jobid":                      jobid,
					"k8s-slurm-injector/last-applied-configuration": string(bytes),
				},
			},
			Immutable: nil,
			Data: map[string]string{
				"env": out,
			},
			BinaryData: nil,
		}
		_, err = s.handler.clientset.CoreV1().ConfigMaps(namespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
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
	jobid := r.URL.Query().Get("jobid")
	s.handler.logger.Infof("state jobid=%s", jobid)

	if jobid == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.handler.logger.Errorf("jobid is not given")
		return
	}

	var state string

	command := ssh_handler.SSHCommand{
		Command: fmt.Sprintf("scontrol show jobid %s", jobid),
	}
	out, err := s.handler.ssh.RunCommand(command)

	// Extract JobState
	reg := regexp.MustCompile(`(?i)JobState=[A-Z]+`)
	matched := reg.Find(out)
	if matched != nil {
		state = strings.Split(string(matched), "=")[1]
	}

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
	jobid := r.URL.Query().Get("jobid")
	namespace := r.URL.Query().Get("namespace")
	objectName := r.URL.Query().Get("objectname")
	configMapName := ConfigMapNameFromObjectName(objectName)
	s.handler.logger.Infof("scancel jobid=%s", jobid)

	if jobid == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.handler.logger.Errorf("jobid is not given")
		return
	}
	if objectName != "" && namespace != "" {
		// Delete the corresponding config-map if exists
		_, err := s.handler.clientset.CoreV1().ConfigMaps(namespace).Get(context.TODO(), configMapName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			s.handler.logger.Warningf("config-map '%s' does not exist, skipped deleting it", configMapName)
		} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
			s.handler.logger.Errorf("error getting configmap %v", statusError.ErrStatus.Message)
		} else if err != nil {
			s.handler.logger.Errorf("error getting configmap: %s", err.Error())
		} else {
			// The corresponding config-map exists
			s.handler.logger.Infof("deleting config-map: %s", configMapName)
			err = s.handler.clientset.CoreV1().ConfigMaps(namespace).Delete(context.TODO(), configMapName, metav1.DeleteOptions{})
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				s.handler.logger.Errorf("error deleting configmap '%s': %s", configMapName, err.Error())
			}
		}
	}

	var out []byte
	var err error = nil

	// Execute `scancel {jobid}`
	command := ssh_handler.SSHCommand{
		Command: fmt.Sprintf("scancel %s", jobid),
	}
	out, err = s.handler.ssh.RunCommand(command)

	// Write to respond
	if err == nil {
		_, _ = fmt.Fprint(w, string(out))
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("failed to scancel: %s", err.Error())
	}
}

func (h handler) scancel() (http.Handler, error) {
	return ScancelHandler{h}, nil
}
