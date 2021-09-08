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
	GetNodeStatus() []NodeStatus
}

type handler struct {
	ssh        ssh_handler.SSHHandler
	nodeInfo   []NodeInfo
	nodeStatus []NodeStatus
}

type dummyHandler struct{}

type NodeInfo struct {
	Node      string
	Partition string
}

type NodeStatus struct {
	Node        string
	NumCPUsFree string
	NumGPUsFree string
	MemFree     string
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

func (h *handler) fetchSlurmNodeStatus() error {
	command := ssh_handler.SSHCommand{
		Command: `
          nodes=$(sinfo --Node | tail -n +2 | awk '{print $1}');
          for node in ${nodes}; do 
            numCPUsFree=$(sinfo -N -o "%N %C" | awk -v node=${node} '{if ($1 == node) {cnt=split($2,cpus,"/"); print cpus[2]}}');
            numGPUs=$(sinfo -N -o "%N %f" | awk -v node=${node} '{if ($1 == node) { if (match($0, /[0-9]+GPUs/)) {print substr($0, RSTART, RLENGTH - 4)} }}');
            numGPUsAllocated=$(squeue -o "%R %b" | awk -v node=${node} '(NR==0){count=0}{if ($1 == node) {if (match($2, "gpu.*[0-9]+")) {cnt=split($2,gres,":"); count += gres[cnt]}}} END{print count}');
            numGPUsFree=$((numGPUs - numGPUsAllocated));
            memFree=$(sinfo -N -o "%N %e" | awk -v node=${node} '($1 == node){print $2}');
            echo "${node} ${numCPUsFree} ${numGPUsFree} ${memFree}";
          done
        `,
	}

	var nodeStatus []NodeStatus
	out, err := h.ssh.RunCommand(command)
	candidates := strings.Split(string(out), "\n")
	for _, candidate := range candidates {
		status := strings.Split(candidate, " ")
		if len(status) != 4 {
			//fmt.Printf("cannot deserialize string: %s, skipped\n", candidate)
			continue
		}
		node := status[0]
		numCPUsFree := status[1]
		numGPUsFree := status[2]
		memFree := status[3]
		if node == "" {
			fmt.Printf("node is empty: %s, skipped\n", candidate)
			continue
		}
		if numCPUsFree == "" {
			fmt.Printf("numCPUsFree is empty: %s, skipped\n", candidate)
			continue
		}
		if numGPUsFree == "" {
			fmt.Printf("numGPUsFree is empty: %s, skipped\n", candidate)
			continue
		}
		if memFree == "" {
			fmt.Printf("memFree is empty: %s, skipped\n", candidate)
			continue
		}

		nodeStatus = append(nodeStatus, NodeStatus{Node: node, NumCPUsFree: numCPUsFree, NumGPUsFree: numGPUsFree, MemFree: memFree})
	}

	if err == nil {
		h.nodeStatus = nodeStatus
	}

	return err
}

func constructCommand(jobInfo *sidecar.JobInformation) ([]string, error) {
	jobName := jobInfo.Name
	if jobName == "" {
		if jobInfo.Namespace == "" || jobInfo.ObjectName == "" {
			jobName = "k8s-slurm-injector-job"
		} else {
			jobName = fmt.Sprintf("%s-%s", jobInfo.Namespace, jobInfo.ObjectName)
		}
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

	return strings.Replace(string(out), "\n", "", -1), err
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

func (h handler) GetNodeStatus() []NodeStatus {
	err := h.fetchSlurmNodeStatus()
	if err != nil {
		return []NodeStatus{}
	}
	return h.nodeStatus
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

func (d dummyHandler) GetNodeStatus() []NodeStatus {
	return []NodeStatus{
		{
			Node:        "node1",
			NumGPUsFree: "1",
		},
	}
}
