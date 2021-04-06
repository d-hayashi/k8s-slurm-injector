package slurm

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	"github.com/d-hayashi/k8s-slurm-injector/internal/ssh_handler"
)

type SbatchHandler struct {
	handler handler
}

type JobEnvHandler struct {
	handler handler
}

type JobStateHandler struct {
	handler handler
}

type ScancelHandler struct {
	handler handler
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
	if ! isPartitionExists {
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

func (s JobEnvHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	jobid := r.URL.Query().Get("jobid")
	s.handler.logger.Infof("state jobid=%s", jobid)

	if jobid == "" {
		s.handler.logger.Errorf("jobid is not given")
		return
	}

	command := ssh_handler.SSHCommand{
		Command: fmt.Sprintf("srun --jobid=%s env", jobid),
	}
	out, err := s.handler.ssh.RunCommand(command)

	// Write to respond
	if err == nil {
		_, _ = fmt.Fprint(w, string(out))
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		s.handler.logger.Errorf("failed to get envrionment variables of job: %s", err.Error())
	}
}

func (h handler) jobEnv() (http.Handler, error) {
	return JobEnvHandler{h}, nil
}

func (s JobStateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	jobid := r.URL.Query().Get("jobid")
	s.handler.logger.Infof("state jobid=%s", jobid)

	if jobid == "" {
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
	s.handler.logger.Infof("scancel jobid=%s", jobid)

	if jobid == "" {
		s.handler.logger.Errorf("jobid is not given")
		return
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
