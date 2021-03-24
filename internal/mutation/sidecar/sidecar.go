package sidecar

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/d-hayashi/k8s-slurm-injector/internal/ssh_handler"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SidecarInjector knows how to mark Kubernetes resources.
type SidecarInjector interface {
	Inject(ctx context.Context, obj metav1.Object) error
	SetNodes(nodes []string)
	SetPartitions(partitions []string)
}

// NewSidecarInjector returns a new sidecar-injector that will inject sidecars.
func NewSidecarInjector(sshHandler ssh_handler.SSHHandler) (SidecarInjector, error) {
	var err error
	injector := sidecarinjector{ssh: sshHandler}
	injector.Nodes, err = injector.fetchSlurmNodes()
	if err == nil {
		injector.Partitions, err = injector.fetchSlurmPartitions()
	}

	return &injector, err
}

type sidecarinjector struct {
	ssh        ssh_handler.SSHHandler
	Nodes      []string
	Partitions []string
}

type JobInformation struct {
	Partition string
	Node      string
	Ntasks    string
	Ncpus     string
	Gres      string
	Time      string
	Name      string
}

func NewJobInformation() *JobInformation {
	jobInfo := JobInformation{
		Partition: "",
		Node:      "",
		Ntasks:    "1",
		Ncpus:     "1",
		Gres:      "",
		Time:      "1440",
		Name:      "",
	}
	return &jobInfo
}

func (s *sidecarinjector) SetNodes(nodes []string) {
	s.Nodes = nodes
}

func (s *sidecarinjector) SetPartitions(partitions []string) {
	s.Partitions = partitions
}

func (s sidecarinjector) isInjectionEnabled(obj metav1.Object) bool {
	// Get labels
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	// Check labels
	isInjection := false
	for key, value := range labels {
		if key == "k8s-slurm-injector/injection" && value == "enabled" {
			isInjection = true
		}
	}

	return isInjection
}

func (s sidecarinjector) validate(obj metav1.Object) error {
	var err error
	var jobInfo JobInformation
	err = s.getJobInformation(obj, &jobInfo)

	if err != nil {
		return err
	}

	isNodeExists := false
	for _, node := range s.Nodes {
		if node == jobInfo.Node {
			isNodeExists = true
		}
	}
	if !isNodeExists {
		return fmt.Errorf("unrecognized node: %s", jobInfo.Node)
	}

	isPartitionExists := false
	for _, partition := range s.Partitions {
		if partition == jobInfo.Partition {
			isPartitionExists = true
		}
	}
	if !isPartitionExists {
		return fmt.Errorf("unrecognized partition: %s", jobInfo.Partition)
	}

	return nil
}

func (s sidecarinjector) getSlurmWebhookURL() string {
	// Get URL
	slurmWebhookHost := os.Getenv("K8S_SLURM_INJECTOR_PORT_8082_TCP_ADDR")
	slurmWebhookPort := os.Getenv("K8S_SLURM_INJECTOR_PORT_8082_TCP_PORT")
	if slurmWebhookHost == "" {
		slurmWebhookHost = "localhost"
	}
	if slurmWebhookPort == "" {
		slurmWebhookPort = "8082"
	}
	slurmWebhookURL := "http://" + slurmWebhookHost + ":" + slurmWebhookPort

	return slurmWebhookURL
}

func (s sidecarinjector) getJobInformation(obj metav1.Object, jobInfo *JobInformation) error {
	var podSpec corev1.PodSpec

	switch v := obj.(type) {
	case *corev1.Pod:
		podSpec = v.Spec
	case *batchv1.Job:
		podSpec = v.Spec.Template.Spec
	case *batchv1beta1.CronJob:
		podSpec = v.Spec.JobTemplate.Spec.Template.Spec
	}

	// Get node name
	if podSpec.NodeName != "" {
		jobInfo.Node = podSpec.NodeName
	}

	// Get ncpus
	ncpus := int64(0)
	for _, container := range podSpec.Containers {
		ncpus = ncpus + container.Resources.Requests.Cpu().Value()
	}
	if ncpus == 0 {
		ncpus = 1
	}
	jobInfo.Ncpus = fmt.Sprintf("%d", ncpus)

	// Get gresp
	ngpus := int64(0)
	for _, container := range podSpec.Containers {
		if val, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
			ngpus = ngpus + val.Value()
		}
	}
	if ngpus > 0 {
		jobInfo.Gres = fmt.Sprintf("gpus:%d", ngpus)
	}

	// Get labels
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	// Get job-information
	for key, value := range labels {
		if key == "k8s-slurm-injector/partition" {
			jobInfo.Partition = value
		}
		if key == "k8s-slurm-injector/node" {
			jobInfo.Node = value
		}
	}

	// Check
	if jobInfo.Node == "" {
		return fmt.Errorf("node must be specified with label 'k8s-slurm-injector/node'")
	}

	return nil
}

func (s sidecarinjector) constructSbatchURL(slurmWebhookURL string, jobInfo JobInformation) string {
	sbatchURL := slurmWebhookURL +
		"/slurm/sbatch?" +
		fmt.Sprintf("partition=%s&", jobInfo.Partition) +
		fmt.Sprintf("node=%s&", jobInfo.Node) +
		fmt.Sprintf("ntasks=%s&", jobInfo.Ntasks) +
		fmt.Sprintf("ncpus=%s&", jobInfo.Ncpus) +
		fmt.Sprintf("gres=%s&", jobInfo.Gres) +
		fmt.Sprintf("time=%s&", jobInfo.Time) +
		fmt.Sprintf("name=%s&", jobInfo.Name)

	return sbatchURL
}

func (s sidecarinjector) mutateObject(obj metav1.Object) error {
	var podSpec corev1.PodSpec
	var jobInfo JobInformation
	var err error

	// Get pod-spec
	switch v := obj.(type) {
	case *corev1.Pod:
		podSpec = v.Spec
	case *batchv1.Job:
		podSpec = v.Spec.Template.Spec
	case *batchv1beta1.CronJob:
		podSpec = v.Spec.JobTemplate.Spec.Template.Spec
	default:
		return fmt.Errorf("unsupported type")
	}

	// Construct sbatch URL
	slurmWebhookURL := s.getSlurmWebhookURL()
	err = s.getJobInformation(obj, &jobInfo)
	if err != nil {
		return fmt.Errorf("failed to get job-information: %s", err.Error())
	}
	sbatchURL := s.constructSbatchURL(slurmWebhookURL, jobInfo)

	// Enable shareProcessNamespace
	shareProcessNamespace := true
	podSpec.ShareProcessNamespace = &shareProcessNamespace

	// Add init-container
	initContainer := corev1.Container{
		Name:    "slurm-injector",
		Image:   "curlimages/curl:7.75.0",
		Command: []string{"/bin/sh", "-c"},
		Args: []string{
			fmt.Sprintf("set -x ; slurmWebhookURL=\"%s\" && sbatchURL=\"%s\" && ", slurmWebhookURL, sbatchURL) +
				"jobid=$(curl -s ${sbatchURL}) && " +
				"echo \"Job ID: ${jobid}\" && " +
				"echo ${jobid} > /k8s-slurm-injector/jobid && " +
				"while true; " +
				"do " +
				"sleep 1; " +
				"echo \"$jobid\" | egrep -q '^[0-9]+$' || exit 1; " +
				"state=$(curl -s ${slurmWebhookURL}/slurm/state?jobid=${jobid}); " +
				"[[ \"$state\" = \"PENDING\" ]] && continue; " +
				"[[ \"$state\" = \"RUNNING\" ]] && break; " +
				"[[ \"$state\" = \"CANCELLED\" ]] && exit 1; " +
				"done",
		},
		ImagePullPolicy: "IfNotPresent",
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "k8s-slurm-injector-shared",
				MountPath: "/k8s-slurm-injector",
			},
		},
	}
	podSpec.InitContainers = append([]corev1.Container{initContainer}, podSpec.InitContainers...)

	// Add hooks to get main container-id
	for i := range podSpec.Containers {
		lifecycle := corev1.Lifecycle{
			PostStart: &corev1.Handler{
				Exec: &corev1.ExecAction{Command: []string{
					"/bin/sh",
					"-c",
					fmt.Sprintf(
						"cat /proc/self/cgroup "+
							"| grep cpuset: "+
							"| awk '{n=split($1,A,\"/\"); print A[n]}'"+
							"> /k8s-slurm-injector/container_id_%d",
						i),
				}},
			},
		}
		volumeMount := corev1.VolumeMount{
			Name:      "k8s-slurm-injector-shared",
			MountPath: "/k8s-slurm-injector",
		}
		podSpec.Containers[i].Lifecycle = &lifecycle
		podSpec.Containers[i].VolumeMounts = append(podSpec.Containers[i].VolumeMounts, volumeMount)
	}

	// Add sidecar
	sidecar := corev1.Container{
		Name:    "slurm-watcher",
		Image:   "curlimages/curl:7.75.0",
		Command: []string{"/bin/sh", "-c"},
		Args: []string{
			fmt.Sprintf(
				"url=%s; "+
					"jobid=$(cat /k8s-slurm-injector/jobid); "+
					"[[ $jobid = \"\" ]] && echo 'Failed to get job-id' >&2 && exit 1; "+
					"[[ $jobid = \"error\" ]] && echo 'Failed to get job-id' >&2 && exit 1; "+
					"getState() { curl -s \"${url}/slurm/state?jobid=${jobid}\"; }; "+
					"scancel() { curl -s \"${url}/slurm/scancel?jobid=${jobid}\"; }; "+
					"trap 'scancel' SIGHUP SIGINT SIGQUIT SIGTERM ; "+
					"count=0; "+
					"while [[ $count -lt 10 ]]; "+
					"do "+
					"[[ -f /k8s-slurm-injector/container_id_0 ]] && break; "+
					"sleep 1; "+
					"count=$((count+1)); "+
					"done; "+
					"[[ $count -ge 10 ]] && exit 1; "+
					"cid=$(cat /k8s-slurm-injector/container_id_0); "+
					"[[ $cid = \"\" ]] && (echo 'Failed to get container_id' >&2; scancel) && exit 1; "+
					"while true; "+
					"do "+
					"sleep 1; "+
					"cat /proc/*/cgroup | grep ${cid} > /dev/null || break; "+
					"state=$(getState); "+
					"[[ \"$state\" = \"COMPLETING\" ]] && break; "+
					"[[ \"$state\" = \"CANCELLED\" ]] && break; "+
					"done; "+
					"for pid in $(cd /proc && echo [0-9]* | tr \" \" \"\\n\" | sort -n); "+
					"do "+
					"cat /proc/${pid}/cgroup | grep ${cid} >/dev/null && kill -9 ${pid} " +
					"&& echo \"Process ${pid} has been killed by slurm.\" >&2; "+
					"done; "+
					"scancel",
				slurmWebhookURL),
		},
		ImagePullPolicy: "IfNotPresent",
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "k8s-slurm-injector-shared",
				MountPath: "/k8s-slurm-injector",
			},
		},
	}
	podSpec.Containers = append(podSpec.Containers, sidecar)

	// Add volume
	volume := corev1.Volume{
		Name: "k8s-slurm-injector-shared",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
	podSpec.Volumes = append([]corev1.Volume{volume}, podSpec.Volumes...)

	switch v := obj.(type) {
	case *corev1.Pod:
		v.Spec = podSpec
	case *batchv1.Job:
		v.Spec.Template.Spec = podSpec
	case *batchv1beta1.CronJob:
		v.Spec.JobTemplate.Spec.Template.Spec = podSpec
	default:
		return fmt.Errorf("unsupported type")
	}

	// Modify annotations
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["k8s-slurm-injector/status"] = "injected"
	annotations["k8s-slurm-injector/partition"] = jobInfo.Partition
	annotations["k8s-slurm-injector/node"] = jobInfo.Node
	annotations["k8s-slurm-injector/ntasks"] = jobInfo.Ntasks
	annotations["k8s-slurm-injector/ncpus"] = jobInfo.Ncpus
	annotations["k8s-slurm-injector/gres"] = jobInfo.Gres
	annotations["k8s-slurm-injector/time"] = jobInfo.Time
	annotations["k8s-slurm-injector/name"] = jobInfo.Name
	obj.SetAnnotations(annotations)

	return nil
}

func (s sidecarinjector) fetchSlurmNodes() ([]string, error) {
	command := ssh_handler.SSHCommand{
		Command: "sinfo --Node | tail -n +2 | awk '{print $1}' | sort | uniq",
	}

	var nodes []string
	out, err := s.ssh.RunCommand(command)
	candidates := strings.Split(string(out), "\n")
	for _, candidate := range candidates {
		if candidate != "" {
			nodes = append(nodes, candidate)
		}
	}

	return nodes, err
}

func (s sidecarinjector) fetchSlurmPartitions() ([]string, error) {
	command := ssh_handler.SSHCommand{
		Command: "sinfo | tail -n +2 | awk '{print $1}' | sort | uniq",
	}

	var partitions []string
	out, err := s.ssh.RunCommand(command)
	candidates := strings.Split(string(out), "\n")
	for _, candidate := range candidates {
		candidate = strings.TrimSuffix(candidate, "*")
		if candidate != "" {
			partitions = append(partitions, candidate)
		}
	}
	partitions = append(partitions, "")

	return partitions, err
}

func (s sidecarinjector) Inject(_ context.Context, obj metav1.Object) error {
	var err error

	// Filter object
	switch obj.(type) {
	case *corev1.Pod:
		// pass
	case *batchv1.Job:
		// pass
	case *batchv1beta1.CronJob:
		// pass
	default:
		return nil
	}

	// Check if injection is enabled
	if !s.isInjectionEnabled(obj) {
		return nil
	}

	// Validate object
	err = s.validate(obj)
	if err != nil {
		return fmt.Errorf("failed to mutate object: %s", err.Error())
	}

	// Mutate object
	err = s.mutateObject(obj)
	if err != nil {
		return fmt.Errorf("failed to mutate object: %s", err.Error())
	}

	return nil
}

// DummyMarker is a marker that doesn't do anything.
var DummySidecarInjector SidecarInjector = dummySidecarInjector(0)

type dummySidecarInjector int

func (dummySidecarInjector) Inject(_ context.Context, _ metav1.Object) error { return nil }
func (dummySidecarInjector) SetNodes(nodes []string)                         {}
func (dummySidecarInjector) SetPartitions(partitions []string)               {}
