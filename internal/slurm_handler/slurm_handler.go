package slurm_handler

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/d-hayashi/k8s-slurm-injector/internal/mutation/sidecar"
	"github.com/d-hayashi/k8s-slurm-injector/internal/ssh_handler"
)

type SlurmHandler interface {
	SBatch(jobInfo *sidecar.JobInformation) (string, error)
	GetEnv(jobid string) (string, error)
	State(jobid string) (string, error)
	SCancel(jobid string) (string, error)
	List() ([]string, error)
	GetNodeInfo() []NodeInfo
}

type handler struct {
	ssh      ssh_handler.SSHHandler
	nodeInfo []NodeInfo
}

type dummyHandler struct{}

type NodeInfo struct {
	Node      string
	Partition string
}

func NewSlurmHandler(ssh ssh_handler.SSHHandler) (SlurmHandler, error) {
	handler := handler{ssh: ssh}
	err := handler.fetchSlurmNodeInfo()
	return &handler, err
}

func NewDummySlurmHandler() (SlurmHandler, error) {
	handler := dummyHandler{}
	return &handler, nil
}

func (h *handler) fetchSlurmNodeInfo() error {
	command := ssh_handler.SSHCommand{
		Command: "sinfo --Node | tail -n +2 | awk '{print $1,$3}'",
	}

	var nodeInfo []NodeInfo
	out, err := h.ssh.RunCommand(command)
	candidates := strings.Split(string(out), "\n")
	for _, candidate := range candidates {
		info := strings.Split(candidate, " ")
		if len(info) != 2 {
			_ = fmt.Errorf("cannot deserialize string: %s, skipped\n", candidate)
			continue
		}
		node := info[0]
		partition := info[1]
		if node == "" {
			_ = fmt.Errorf("node is empty: %s, skipped\n", candidate)
			continue
		}
		if partition == "" {
			_ = fmt.Errorf("partition is empty: %s, skipped\n", candidate)
			continue
		}
		partition = strings.TrimSuffix(partition, "*")

		nodeInfo = append(nodeInfo, NodeInfo{Node: node, Partition: partition})
		fmt.Printf("registered a node: node=%s, partition=%s\n", node, partition)
	}

	if err == nil {
		h.nodeInfo = nodeInfo
	}

	return err
}

func constructCommand(jobInfo *sidecar.JobInformation) ([]string, error) {
	jobName := jobInfo.Name
	if jobName == "" {
		jobName = "k8s-slurm-injector-job"
	}

	commands := []string{
		"sbatch",
		"--parsable",
		"--output=/dev/null",
		"--error=/dev/null",
		fmt.Sprintf("--job-name=%s", jobName),
	}
	if jobInfo.Partition != "" {
		commands = append(commands, fmt.Sprintf("--partition=%s", jobInfo.Partition))
	}
	if jobInfo.Node != "" {
		commands = append(commands, fmt.Sprintf("--nodelist=%s", jobInfo.Node))
	}
	if jobInfo.Ntasks != "" {
		commands = append(commands, fmt.Sprintf("--ntasks=%s", jobInfo.Ntasks))
	}
	if jobInfo.Ncpus != "" {
		commands = append(commands, fmt.Sprintf("--cpus-per-task=%s", jobInfo.Ncpus))
	}
	if jobInfo.Gres != "" {
		commands = append(commands, fmt.Sprintf("--gres=%s", jobInfo.Gres))
	}
	if jobInfo.Time != "" {
		commands = append(commands, fmt.Sprintf("--time=%s", jobInfo.Time))
	}

	return commands, nil
}

func (h handler) SBatch(jobInfo *sidecar.JobInformation) (string, error) {
	var out []byte
	commands, err := constructCommand(jobInfo)

	// Execute ssh commands
	if err == nil {
		command := ssh_handler.SSHCommand{
			Command: strings.Join(commands, " "),
			StdinPipe: "#!/bin/sh " +
				"\n trap 'echo \"Killing...\"' SIGHUP SIGINT SIGQUIT SIGTERM " +
				"\n echo \"Sleeping.  Pid=$$\" " +
				"\n while true; do sleep 10 & wait $!; done",
		}
		out, err = h.ssh.RunCommandCombined(command)
	}

	return string(out), err
}

func (h handler) GetEnv(jobid string) (string, error) {
	if jobid == "" {
		return "", fmt.Errorf("jobid is not given")
	}

	command := ssh_handler.SSHCommand{
		Command: fmt.Sprintf("srun --jobid=%s env", jobid),
	}
	out, err := h.ssh.RunCommand(command)

	return string(out), err
}

func (h handler) State(jobid string) (string, error) {
	if jobid == "" {
		return "", fmt.Errorf("jobid is not given")
	}

	var state string

	command := ssh_handler.SSHCommand{
		Command: fmt.Sprintf("scontrol show jobid %s", jobid),
	}
	out, err := h.ssh.RunCommand(command)

	// Extract JobState
	reg := regexp.MustCompile(`(?i)JobState=[A-Z]+`)
	matched := reg.Find(out)
	if matched != nil {
		state = strings.Split(string(matched), "=")[1]
	}

	return state, err
}

func (h handler) SCancel(jobid string) (string, error) {
	if jobid == "" {
		return "", fmt.Errorf("jobid is not given")
	}

	// Execute `scancel {jobid}`
	command := ssh_handler.SSHCommand{
		Command: fmt.Sprintf("scancel %s", jobid),
	}
	out, err := h.ssh.RunCommand(command)

	return string(out), err
}

func (h handler) List() ([]string, error) {
	var jobIds []string

	command := ssh_handler.SSHCommand{
		Command: "squeue -u $USER -o \"%i\" --noheader",
	}
	out, err := h.ssh.RunCommand(command)

	if err == nil {
		for _, jobId := range strings.Split(string(out), "\n") {
			if jobId != "" {
				jobIds = append(jobIds, jobId)
			}
		}
	}

	return jobIds, err
}

func (h handler) GetNodeInfo() []NodeInfo {
	return h.nodeInfo
}

func (d dummyHandler) SBatch(_ *sidecar.JobInformation) (string, error) {
	return "1234567890", nil
}

func (d dummyHandler) GetEnv(jobid string) (string, error) {
	if jobid == "" {
		return "", fmt.Errorf("jobid is not given")
	}

	return "FOO=BAR", nil
}

func (d dummyHandler) State(jobid string) (string, error) {
	if jobid == "" {
		return "", fmt.Errorf("jobid is not given")
	}

	return "STATE", nil
}

func (d dummyHandler) SCancel(jobid string) (string, error) {
	if jobid == "" {
		return "", fmt.Errorf("jobid is not given")
	}

	return "", nil
}

func (h dummyHandler) List() ([]string, error) {
	return []string{}, nil
}

func (d dummyHandler) GetNodeInfo() []NodeInfo {
	return []NodeInfo{
		{
			Node:      "node1",
			Partition: "partition1",
		},
	}
}
