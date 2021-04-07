package sidecar

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/ssh_handler"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SidecarInjector knows how to mark Kubernetes resources.
type SidecarInjector interface {
	Inject(ctx context.Context, obj metav1.Object) (string, error)
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
	Namespace             string
	ObjectName            string
	NodeSpecificationMode string
	Partition             string
	Node                  string
	Ntasks                string
	Ncpus                 string
	GpuLimit              bool
	Gres                  string
	Time                  string
	Name                  string
}

func NewJobInformation() *JobInformation {
	jobInfo := JobInformation{
		Namespace:             "",
		ObjectName:            "",
		NodeSpecificationMode: "auto",
		Partition:             "",
		Node:                  "",
		Ntasks:                "1",
		Ncpus:                 "1",
		GpuLimit:              false,
		Gres:                  "",
		Time:                  "1440",
		Name:                  "",
	}
	return &jobInfo
}

func (s *sidecarinjector) SetNodes(nodes []string) {
	s.Nodes = nodes
}

func (s *sidecarinjector) SetPartitions(partitions []string) {
	s.Partitions = partitions
}

func IsInjectionEnabled(obj metav1.Object) bool {
	var podSpec corev1.PodSpec
	isInjection := false

	// Get labels
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	// Check labels
	for key, value := range labels {
		if key == "k8s-slurm-injector/injection" && value == "enabled" {
			isInjection = true
		}
	}

	// Get pod-spec
	switch v := obj.(type) {
	case *corev1.Pod:
		podSpec = v.Spec
	case *batchv1.Job:
		podSpec = v.Spec.Template.Spec
	case *batchv1beta1.CronJob:
		podSpec = v.Spec.JobTemplate.Spec.Template.Spec
	default:
		return false
	}

	// Check environment variables
	for _, container := range podSpec.Containers {
		for _, env := range container.Env {
			if env.Name == "K8S_SLURM_INJECTOR_INJECTION" && env.Value == "enabled" {
				isInjection = true
			}
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

	if jobInfo.NodeSpecificationMode != "manual" {
		return nil
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
	namespace := ""
	objectname := ""
	labels := obj.GetLabels()
	ntasks := int64(1)
	ncpus := int64(0)
	ngpus := int64(0)
	time := int64(1440)
	name := ""

	switch v := obj.(type) {
	case *corev1.Pod:
		podSpec = v.Spec
		namespace = v.Namespace
		objectname = fmt.Sprintf("pod-%s", v.Name)
	case *batchv1.Job:
		podSpec = v.Spec.Template.Spec
		namespace = v.Namespace
		objectname = fmt.Sprintf("job-%s", v.Name)
	case *batchv1beta1.CronJob:
		podSpec = v.Spec.JobTemplate.Spec.Template.Spec
		namespace = v.Namespace
		objectname = fmt.Sprintf("cronjob-%s", v.Name)
	}
	jobInfo.Namespace = namespace
	jobInfo.ObjectName = objectname

	// Get labels
	if labels == nil {
		labels = map[string]string{}
	}

	// Get job-information
	for key, value := range labels {
		if key == "k8s-slurm-injector/node-specification-mode" {
			jobInfo.NodeSpecificationMode = value
			if value != "auto" && value != "manual" {
				return fmt.Errorf("unrecognized label 'k8s-slurm-injector/node-specification-mode=%s'", value)
			}
		}
		if key == "k8s-slurm-injector/partition" {
			jobInfo.Partition = value
		}
		if key == "k8s-slurm-injector/node" {
			jobInfo.Node = value
		}
		if key == "k8s-slurm-injector/ntasks" {
			ntasks, _ = strconv.ParseInt(value, 10, 64)
		}
		if key == "k8s-slurm-injector/ncpus" {
			ncpus, _ = strconv.ParseInt(value, 10, 64)
		}
		if key == "k8s-slurm-injector/ngpus" {
			ngpus, _ = strconv.ParseInt(value, 10, 64)
		}
		if key == "k8s-slurm-injector/time" {
			time, _ = strconv.ParseInt(value, 10, 64)
		}
		if key == "k8s-slurm-injector/name" {
			name = value
		}
	}

	// Get node name
	if podSpec.NodeName != "" {
		jobInfo.Node = podSpec.NodeName
	}

	// Auto mode
	if jobInfo.NodeSpecificationMode != "manual" {
		jobInfo.Partition = ""
		jobInfo.Node = "::K8S_SLURM_INJECTOR_NODE::"
	}

	// Retrieve from environment variables
	for _, container := range podSpec.Containers {
		for _, env := range container.Env {
			if env.Name == "K8S_SLURM_INJECTOR_NODE_SPECIFICATION_MODE" {
				jobInfo.NodeSpecificationMode = env.Value
				if env.Value != "auto" && env.Value != "manual" {
					return fmt.Errorf("unrecognized value '%s=%s'", env.Name, env.Value)
				}
			}
			if env.Name == "K8S_SLURM_INJECTOR_PARTITION" {
				jobInfo.Partition = env.Value
			}
			if env.Name == "K8S_SLURM_INJECTOR_NODE" {
				jobInfo.Node = env.Value
			}
			if env.Name == "K8S_SLURM_INJECTOR_NTASKS" {
				ntasks, _ = strconv.ParseInt(env.Value, 10, 64)
			}
			if env.Name == "K8S_SLURM_INJECTOR_NCPUS" {
				ncpus, _ = strconv.ParseInt(env.Value, 10, 64)
			}
			if env.Name == "K8S_SLURM_INJECTOR_NGPUS" {
				ngpus, _ = strconv.ParseInt(env.Value, 10, 64)
			}
			if env.Name == "K8S_SLURM_INJECTOR_TIME" {
				time, _ = strconv.ParseInt(env.Value, 10, 64)
			}
			if env.Name == "K8S_SLURM_INJECTOR_NAME" {
				name = env.Value
			}
		}
	}
	jobInfo.Ntasks = fmt.Sprintf("%d", ntasks)
	jobInfo.Time = fmt.Sprintf("%d", time)
	jobInfo.Name = name

	// Get ncpus
	for _, container := range podSpec.Containers {
		ncpus = ncpus + container.Resources.Requests.Cpu().Value()
	}
	if ncpus == 0 {
		ncpus = 1
	}
	jobInfo.Ncpus = fmt.Sprintf("%d", ncpus)

	// Get gresp
	for _, container := range podSpec.Containers {
		if val, ok := container.Resources.Limits["nvidia.com/gpu"]; ok {
			ngpus = ngpus + val.Value()
			jobInfo.GpuLimit = true
		}
	}
	if ngpus > 0 {
		jobInfo.Gres = fmt.Sprintf("gpu:%d", ngpus)
	}

	// Check
	if jobInfo.NodeSpecificationMode == "manual" && jobInfo.Node == "" {
		return fmt.Errorf("you must either use auto-injection mode or manually specify a node with label 'k8s-slurm-injector/node'")
	}

	return nil
}

func (s sidecarinjector) constructURL(slurmWebhookURL string, jobInfo JobInformation, action string) string {
	url := slurmWebhookURL + "/slurm/" + action + "?" +
		fmt.Sprintf("namespace=%s&", jobInfo.Namespace) +
		fmt.Sprintf("objectname=%s&", jobInfo.ObjectName) +
		fmt.Sprintf("nodespecificationmode=%s&", jobInfo.NodeSpecificationMode) +
		fmt.Sprintf("partition=%s&", jobInfo.Partition) +
		fmt.Sprintf("node=%s&", jobInfo.Node) +
		fmt.Sprintf("ntasks=%s&", jobInfo.Ntasks) +
		fmt.Sprintf("ncpus=%s&", jobInfo.Ncpus) +
		fmt.Sprintf("gres=%s&", jobInfo.Gres) +
		fmt.Sprintf("time=%s&", jobInfo.Time) +
		fmt.Sprintf("name=%s&", jobInfo.Name)

	url = strings.TrimSuffix(url, "&")

	return url
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

	// Construct URLs
	slurmWebhookURL := s.getSlurmWebhookURL()
	err = s.getJobInformation(obj, &jobInfo)
	if err != nil {
		return fmt.Errorf("failed to get job-information: %s", err.Error())
	}
	sbatchURL := s.constructURL(slurmWebhookURL, jobInfo, "sbatch")
	stateURL := s.constructURL(slurmWebhookURL, jobInfo, "state")
	envURL := s.constructURL(slurmWebhookURL, jobInfo, "envtoconfigmap")
	scancelURL := s.constructURL(slurmWebhookURL, jobInfo, "scancel")
	configMapName := config_map.ConfigMapNameFromObjectName(jobInfo.ObjectName)

	// Enable shareProcessNamespace
	shareProcessNamespace := true
	podSpec.ShareProcessNamespace = &shareProcessNamespace

	// Add init-container
	initContainer := corev1.Container{
		Name:    "slurm-injector",
		Image:   "curlimages/curl:7.75.0",
		Command: []string{"/bin/sh", "-c"},
		Env: []corev1.EnvVar{
			{
				Name: "K8S_SLURM_INJECTOR_NODE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "spec.nodeName",
					},
				},
			},
		},
		Args: []string{
			"set -x; " +
				fmt.Sprintf("export stateURL=\"%s\" && ", stateURL) +
				fmt.Sprintf("export sbatchURL=\"%s\" && ", sbatchURL) +
				fmt.Sprintf("export scancelURL=\"%s\"; ", scancelURL) +
				fmt.Sprintf("export nodeName=\"%s\"; ", jobInfo.Node) +
				"if [[ ${nodeName} = \"::K8S_SLURM_INJECTOR_NODE::\" ]]; " +
				"then " +
				"sbatchURL=$(echo ${sbatchURL} | sed \"s/::K8S_SLURM_INJECTOR_NODE::/${K8S_SLURM_INJECTOR_NODE}/\"); " +
				"fi; " +
				"export jobid=$(curl -s ${sbatchURL}) && " +
				"echo \"Job ID: ${jobid}\" && " +
				"echo ${jobid} > /k8s-slurm-injector/jobid ; " +
				"scancel() { curl -s \"${scancelURL}&jobid=${jobid}\"; }; " +
				"trap 'scancel || exit 0' SIGHUP SIGINT SIGQUIT SIGTERM ; " +
				"while true; " +
				"do " +
				"sleep 1; " +
				"echo \"$jobid\" | egrep -q '^[0-9]+$' || exit 1; " +
				"state=$(curl -s \"${stateURL}&jobid=${jobid}\"); " +
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

	// Add init-container-2
	initContainer2 := corev1.Container{
		Name:    "slurm-pipeline",
		Image:   "curlimages/curl:7.75.0",
		Command: []string{"/bin/sh", "-c"},
		Args: []string{
			"set -x; " +
				fmt.Sprintf("export envURL=\"%s\"; ", envURL) +
				fmt.Sprintf("export scancelURL=\"%s\"; ", scancelURL) +
				"export jobid=$(cat /k8s-slurm-injector/jobid); " +
				"scancel() { curl -s \"${scancelURL}&jobid=${jobid}\"; }; " +
				"[[ $jobid = \"\" ]] && echo 'Failed to get job-id' >&2 && exit 1; " +
				"[[ $jobid = \"error\" ]] && echo 'Failed to get job-id' >&2 && exit 1; " +
				"trap 'scancel || exit 0' SIGHUP SIGINT SIGQUIT SIGTERM ; " +
				"curl -s \"${envURL}&jobid=${jobid}\" > /k8s-slurm-injector/env || scancel",
		},
		ImagePullPolicy: "IfNotPresent",
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "k8s-slurm-injector-shared",
				MountPath: "/k8s-slurm-injector",
			},
		},
	}
	podSpec.InitContainers = append([]corev1.Container{initContainer, initContainer2}, podSpec.InitContainers...)

	// Mutate containers
	for i := range podSpec.Containers {
		// Add a script to get container-id
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

		// Set environment variables allocated by Slurm
		optional := true
		podSpec.Containers[i].EnvFrom = append(podSpec.Containers[i].EnvFrom,
			corev1.EnvFromSource{
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
					Optional: &optional,
				},
			},
		)
	}

	// Add container `slurm-watcher` as sidecar
	sidecar := corev1.Container{
		Name:    "slurm-watcher",
		Image:   "curlimages/curl:7.75.0",
		Command: []string{"/bin/sh", "-c"},
		Args: []string{
			fmt.Sprintf("stateURL=\"%s\"; ", stateURL) +
				fmt.Sprintf("scancelURL=\"%s\"; ", scancelURL) +
				"jobid=$(cat /k8s-slurm-injector/jobid); " +
				"[[ $jobid = \"\" ]] && echo 'Failed to get job-id' >&2 && exit 1; " +
				"[[ $jobid = \"error\" ]] && echo 'Failed to get job-id' >&2 && exit 1; " +
				"getState() { curl -s \"${stateURL}&jobid=${jobid}\"; }; " +
				"scancel() { curl -s \"${scancelURL}&jobid=${jobid}\"; }; " +
				"trap 'scancel || exit 0' SIGHUP SIGINT SIGQUIT SIGTERM ; " +
				"count=0; " +
				"while [[ $count -lt 10 ]]; " +
				"do " +
				"[[ -f /k8s-slurm-injector/container_id_0 ]] && break; " +
				"sleep 1; " +
				"count=$((count+1)); " +
				"done; " +
				"[[ $count -ge 10 ]] && scancel && exit 1; " +
				"cid=$(cat /k8s-slurm-injector/container_id_0); " +
				"[[ $cid = \"\" ]] && (echo 'Failed to get container_id' >&2; scancel) && exit 1; " +
				"while true; " +
				"do " +
				"sleep 1; " +
				"cat /proc/*/cgroup | grep ${cid} > /dev/null || break; " +
				"state=$(getState); " +
				"[[ \"$state\" = \"COMPLETING\" ]] && break; " +
				"[[ \"$state\" = \"CANCELLED\" ]] && break; " +
				"done; " +
				"for pid in $(cd /proc && echo [0-9]* | tr \" \" \"\\n\" | sort -n); " +
				"do " +
				"cat /proc/${pid}/cgroup | grep ${cid} >/dev/null && kill -9 ${pid} " +
				"&& echo \"Process ${pid} has been killed by slurm.\" >&2; " +
				"done; " +
				"scancel || exit 0",
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

	// Apply mutations
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
	annotations["k8s-slurm-injector/namespace"] = jobInfo.Namespace
	annotations["k8s-slurm-injector/object-name"] = jobInfo.ObjectName
	annotations["k8s-slurm-injector/node-specification-mode"] = jobInfo.NodeSpecificationMode
	annotations["k8s-slurm-injector/partition"] = jobInfo.Partition
	annotations["k8s-slurm-injector/node"] = jobInfo.Node
	annotations["k8s-slurm-injector/ntasks"] = jobInfo.Ntasks
	annotations["k8s-slurm-injector/ncpus"] = jobInfo.Ncpus
	annotations["k8s-slurm-injector/gpu-limit"] = strconv.FormatBool(jobInfo.GpuLimit)
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

func (s sidecarinjector) Inject(_ context.Context, obj metav1.Object) (string, error) {
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
		return "", nil
	}

	// Check if injection is enabled
	if !IsInjectionEnabled(obj) {
		return "", nil
	}

	// Validate object
	err = s.validate(obj)
	if err != nil {
		return "", fmt.Errorf("failed to mutate object: %s", err.Error())
	}

	// Mutate object
	err = s.mutateObject(obj)
	if err != nil {
		return "", fmt.Errorf("failed to mutate object: %s", err.Error())
	}

	return "Sidecar for SLURM injected", nil
}

// DummyMarker is a marker that doesn't do anything.
var DummySidecarInjector SidecarInjector = dummySidecarInjector(0)

type dummySidecarInjector int

func (dummySidecarInjector) Inject(_ context.Context, _ metav1.Object) (string, error) {
	return "", nil
}
func (dummySidecarInjector) SetNodes(nodes []string)           {}
func (dummySidecarInjector) SetPartitions(partitions []string) {}
