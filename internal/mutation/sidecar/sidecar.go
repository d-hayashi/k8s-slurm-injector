package sidecar

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/d-hayashi/k8s-slurm-injector/internal/client_set"
	"github.com/d-hayashi/k8s-slurm-injector/internal/config_map"
	"github.com/d-hayashi/k8s-slurm-injector/internal/ssh_handler"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SidecarInjector knows how to mark Kubernetes resources.
type SidecarInjector interface {
	Inject(ctx context.Context, obj metav1.Object) (string, error)
	SetNodes(nodes []string)
	SetPartitions(partitions []string)
}

// NewSidecarInjector returns a new sidecar-injector that will inject sidecars.
func NewSidecarInjector(
	sshHandler ssh_handler.SSHHandler,
	configMapHandler config_map.ConfigMapHandler,
	targetNamespaces []string,
) (SidecarInjector, error) {
	var err error
	injector := sidecarinjector{ssh: sshHandler, configMapHandler: configMapHandler, TargetNamespaces: targetNamespaces}
	injector.Nodes, err = injector.fetchSlurmNodes()
	if err == nil {
		injector.Partitions, err = injector.fetchSlurmPartitions()
	}

	return &injector, err
}

type sidecarinjector struct {
	ssh              ssh_handler.SSHHandler
	configMapHandler config_map.ConfigMapHandler
	Nodes            []string
	Partitions       []string
	TargetNamespaces []string
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
		Time:                  "",
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

func IsInjectionEnabled(obj metav1.Object, targetNamespaces []string, objectNamespace string) bool {
	var podSpec corev1.PodSpec
	isInjectionEnabled := false

	// Get labels
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	// Check labels
	hasExperimentLabel := false
	hasSuggestionLabel := false
	for key, value := range labels {
		if key == "k8s-slurm-injector/injection" && value == "enabled" {
			isInjectionEnabled = true
		}
		if key == "k8s-slurm-injector/injection" && value == "disabled" {
			return false
		}
		if key == "experiment" {
			hasExperimentLabel = true
		}
		if key == "suggestion" {
			hasSuggestionLabel = true
		}
	}
	if hasExperimentLabel && hasSuggestionLabel {
		fmt.Printf("this pod seems to be a part of Katib's resources, so skipped injecting slurm jobs")
		return false
	}

	// Get annotations
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	// Check labels
	for key, value := range annotations {
		if key == "k8s-slurm-injector/status" && value == "injected" {
			return false
		}
	}

	// Check namespaces
	var namespace string
	var err error
	if objectNamespace != "" {
		namespace = objectNamespace
	} else {
		namespace, err = getObjectNamespace(obj)
		if err != nil {
			fmt.Printf("failed to get namespace of the object: %s", err)
		}
	}
	for _, targetNamespace := range targetNamespaces {
		if match, _ := regexp.MatchString(targetNamespace, namespace); match {
			isInjectionEnabled = true
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
				isInjectionEnabled = true
			}
			if env.Name == "K8S_SLURM_INJECTOR_INJECTION" && env.Value == "disabled" {
				return false
			}
		}
	}

	return isInjectionEnabled
}

func getObjectNamespace(obj metav1.Object) (string, error) {
	namespace := obj.GetNamespace()
	if namespace != "" {
		return namespace, nil
	}

	// Get namespace by referring ownerReferences
	clientset := client_set.GetClientSet()
	for _, ownerReference := range obj.GetOwnerReferences() {
		if ownerReference.Kind == "ReplicaSet" {
			allReplicaSets, err := clientset.AppsV1().ReplicaSets("").List(context.TODO(), metav1.ListOptions{
				TypeMeta: metav1.TypeMeta{
					Kind:       ownerReference.Kind,
					APIVersion: ownerReference.APIVersion,
				},
				LabelSelector:        "",
				FieldSelector:        fmt.Sprintf("metadata.name=%s", ownerReference.Name),
				Watch:                false,
				AllowWatchBookmarks:  false,
				ResourceVersion:      "",
				ResourceVersionMatch: "",
				TimeoutSeconds:       nil,
				Limit:                0,
				Continue:             "",
			})
			if err != nil {
				return "", fmt.Errorf("failed to get replicasets: %s", err)
			}
			if len(allReplicaSets.Items) == 0 {
				fmt.Printf("failed to get the corresponding replicasets to object: %s", ownerReference.Name)
				continue
			} else if len(allReplicaSets.Items) > 1 {
				return "", fmt.Errorf("more than one replicasets were found for object: %s", ownerReference.Name)
			} else {
				fmt.Printf("found the corresponding replicaset: %s", allReplicaSets.Items[0].Name)
				return allReplicaSets.Items[0].Namespace, nil
			}
		}
	}

	// Get namespace from labels
	labels := obj.GetLabels()
	for key, value := range labels {
		if key == "k8s-slurm-injector/namespace" {
			return value, nil
		}
	}

	return "", fmt.Errorf("failed to get namespace of object: %s", obj)
}

func getObjectName(obj metav1.Object) (string, error) {
	name := obj.GetName()
	if name != "" {
		return name, nil
	}

	// Generate name
	generateName := obj.GetGenerateName()
	if generateName == "" {
		return "", fmt.Errorf("failed to get GenerateName")
	}

	name = generateName + strconv.FormatInt(obj.GetGeneration(), 10)

	return name, nil
}

func (s sidecarinjector) validate(obj metav1.Object, objectNamespace string) error {
	var err error
	var jobInfo JobInformation
	err = s.getJobInformation(obj, &jobInfo, objectNamespace)

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

func (s sidecarinjector) getJobInformation(obj metav1.Object, jobInfo *JobInformation, objectNamespace string) error {
	var podSpec corev1.PodSpec
	namespace := objectNamespace
	objectname, err := getObjectName(obj)
	if err != nil {
		return fmt.Errorf("failed to get name of object: %s", obj)
	}
	labels := obj.GetLabels()
	ntasks := int64(1)
	ncpus := int64(0)
	ngpus := int64(0)
	time := int64(0)
	name := ""

	switch v := obj.(type) {
	case *corev1.Pod:
		podSpec = v.Spec
		objectname = fmt.Sprintf("pod-%s", objectname)
	case *batchv1.Job:
		podSpec = v.Spec.Template.Spec
		objectname = fmt.Sprintf("job-%s", objectname)
	case *batchv1beta1.CronJob:
		podSpec = v.Spec.JobTemplate.Spec.Template.Spec
		objectname = fmt.Sprintf("cronjob-%s", objectname)
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

	if ntasks > 0 {
		jobInfo.Ntasks = fmt.Sprintf("%d", ntasks)
	}
	if time > 0 {
		jobInfo.Time = fmt.Sprintf("%d", time)
	}
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

func (s sidecarinjector) mutateObject(obj metav1.Object, objectNamespace string) error {
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
	err = s.getJobInformation(obj, &jobInfo, objectNamespace)
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
	var recognizedSidecarContainerIndices []int
	for i := range podSpec.Containers {
		// Add a script to get container-id in case of sidecar "istio-proxy"
		if podSpec.Containers[i].Name == "istio-proxy" {
			lifecycle := corev1.Lifecycle{
				PostStart: &corev1.Handler{
					Exec: &corev1.ExecAction{Command: []string{
						"/bin/sh",
						"-c",
						fmt.Sprintf(
							"cat /proc/self/cgroup "+
								"| grep cpuset: "+
								"| awk '{n=split($1,A,\"/\"); print A[n]}'"+
								"> /k8s-slurm-injector/sidecar_container_id_%d",
							i),
					}},
				},
			}
			podSpec.Containers[i].Lifecycle = &lifecycle
			recognizedSidecarContainerIndices = append(recognizedSidecarContainerIndices, i)
		}

		// Mount shared-volumes
		volumeMount := corev1.VolumeMount{
			Name:      "k8s-slurm-injector-shared",
			MountPath: "/k8s-slurm-injector",
		}
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

		// Remove `spec.containers[].resources.limits.nvidia.com/gpu`
		delete(podSpec.Containers[i].Resources.Limits, "nvidia.com/gpu")
		// Remove `spec.containers[].resources.requests.nvidia.com/gpu`
		delete(podSpec.Containers[i].Resources.Requests, "nvidia.com/gpu")
	}

	// Add container `slurm-watcher` as sidecar
	recognizedSidecarContainerCheck := ""
	for _, idx := range recognizedSidecarContainerIndices {
		recognizedSidecarContainerCheck +=
			fmt.Sprintf(
				"count=0; "+
					"while [[ $count -lt 10 ]]; "+
					"do "+
					"[[ -f /k8s-slurm-injector/sidecar_container_id_%d ]] && break; "+
					"sleep 1; "+
					"done; ",
				idx,
			)
	}
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
				"touch /k8s-slurm-injector/cids_all; " +
				"cat /proc/self/cgroup | grep -q containerd; " +
				"if [ $? -eq 0 ]; then is_containerd=true; else is_containerd=false; done" +
				"if ${is_containerd}; " +
				"then cid_self=$(cat /proc/self/cgroup | grep cpuset: | awk '{n=split($1,A,\":\"); print A[n]}'); " +
				"else cid_self=$(cat /proc/self/cgroup | grep cpuset: | awk '{n=split($1,A,\"/\"); print A[n]}'); " +
				"fi; " +
				recognizedSidecarContainerCheck +
				"count=0; " +
				"while [[ $count -lt 3 ]]; " +
				"do " +
				"if ${is_containerd};" +
				"then cat /proc/*/cgroup | awk '{n=split($1,A,\":\"); print A[n]}' " +
				"| sort | uniq | awk 'length($0)==64{print $0}' >> /k8s-slurm-injector/cids_all; " +
				"else cat /proc/*/cgroup | awk '{n=split($1,A,\"/\"); print A[n]}' " +
				"| sort | uniq | awk 'length($0)==64{print $0}' >> /k8s-slurm-injector/cids_all; " +
				"fi; " +
				"sleep 1; " +
				"count=$((count+1)); " +
				"done; " +
				"cat /k8s-slurm-injector/cids_all | sort | uniq | grep -v ${cid_self} > /k8s-slurm-injector/cids; " +
				"cids=$(cat /k8s-slurm-injector/cids);" +
				"[[ ! -n \"$cids\" ]] && (echo 'Failed to get container_id' >&2; scancel) && exit 1; " +
				"pid_sleep=$(ps aux | grep /pause | grep root | head -n1 | awk '{print $1}'); " +
				"while [[ -n \"$cids\" ]]; " +
				"do " +
				"cid=$(cat /k8s-slurm-injector/cids | head -n1); " +
				"cat /k8s-slurm-injector/cids | tail -n +2 > /k8s-slurm-injector/cids.new;" +
				"mv /k8s-slurm-injector/cids.new /k8s-slurm-injector/cids; " +
				"cids=$(cat /k8s-slurm-injector/cids);" +
				"cat /k8s-slurm-injector/sidecar_container_id_* | grep ${cid} && continue; " +
				"while true; " +
				"do " +
				"sleep 1; " +
				"cat $(find /proc -mindepth 2 -maxdepth 2 -type f -name cgroup | grep -v /proc/${pid_sleep}/cgroup) " +
				"| grep ${cid} > /dev/null || break; " +
				"done; " +
				"for pid in $(cd /proc && echo [0-9]* | tr \" \" \"\\n\" | sort -n); " +
				"do " +
				"[[ $pid -eq $pid_sleep ]] && continue; " +
				"cat /proc/${pid}/cgroup | grep ${cid} >/dev/null && kill -9 ${pid} " +
				"&& echo \"Process ${pid} has been killed by slurm.\" >&2; " +
				"done; " +
				"done; " +
				"echo 'Terminating Istio-pilot if exists'; " +
				"curl -fsI -X POST http://localhost:15020/quitquitquit; " +
				"cat /k8s-slurm-injector/sidecar_container_id_* > /k8s-slurm-injector/sidecar_container_ids; " +
				"cids=$(cat /k8s-slurm-injector/sidecar_container_ids); " +
				"while [[ -n \"$cids\" ]]; " +
				"do " +
				"cid=$(cat /k8s-slurm-injector/sidecar_container_ids | head -n1); " +
				"cat /k8s-slurm-injector/sidecar_container_ids | tail -n +2 > /k8s-slurm-injector/sidecar_container_ids.new; " +
				"mv /k8s-slurm-injector/sidecar_container_ids.new /k8s-slurm-injector/sidecar_container_ids; " +
				"cids=$(cat /k8s-slurm-injector/sidecar_container_ids); " +
				"for pid in $(cd /proc && echo [0-9]* | tr \" \" \"\\n\" | sort -n); " +
				"do " +
				"[[ $pid -eq $pid_sleep ]] && continue; " +
				"cat /proc/${pid}/cgroup | grep ${cid} >/dev/null && kill -9 ${pid}; " +
				"done; " +
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
		Lifecycle: &corev1.Lifecycle{
			PreStop: &corev1.Handler{
				Exec: &corev1.ExecAction{Command: []string{
					"/bin/sh",
					"-c",
					fmt.Sprintf(
						"jobid=$(cat /k8s-slurm-injector/jobid); curl -s \"%s&jobid=${jobid}\"", scancelURL,
					),
				}},
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

	// Modify labels
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["k8s-slurm-injector/status"] = "injected"
	labels["k8s-slurm-injector/namespace"] = jobInfo.Namespace
	labels["k8s-slurm-injector/object-name"] = jobInfo.ObjectName
	obj.SetLabels(labels)

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

func (s sidecarinjector) createConfigMap(obj metav1.Object, objectNamespace string) error {
	namespace := objectNamespace
	annotations := obj.GetAnnotations()
	objectName, exist := annotations["k8s-slurm-injector/object-name"]
	if !exist {
		return fmt.Errorf("annotation 'k8s-slurm-injector/object-name' not found")
	}
	configMapName := config_map.ConfigMapNameFromObjectName(objectName)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "k8s-slurm-injector",
			},
			Annotations: map[string]string{
				"k8s-slurm-injector/last-applied-command": "inject",
				"k8s-slurm-injector/namespace":            namespace,
				"k8s-slurm-injector/object-name":          objectName,
			},
		},
	}

	_, err := s.configMapHandler.GetConfigMap(namespace, configMapName, nil)
	if errors.IsNotFound(err) {
		// Create a config-map
		_, err := s.configMapHandler.CreateConfigMap(namespace, cm, nil)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		// Update config-map
		_, err = s.configMapHandler.UpdateConfigMap(namespace, cm, nil)
		if err != nil {
			return err
		}
	}

	return nil
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

	// Check labels
	labels := obj.GetLabels()
	for key, value := range labels {
		if key == "k8s-slurm-injector/injection" && value == "disabled" {
			return "", nil
		}
	}

	// Get object's namespace
	objectNamespace, err := getObjectNamespace(obj)
	if err != nil {
		return "", fmt.Errorf("failed to get object's namespace: %s", err)
	}

	// Check if injection is enabled
	if !IsInjectionEnabled(obj, s.TargetNamespaces, objectNamespace) {
		fmt.Printf("Ibjection is not enabled: %s", obj)
		return "", nil
	}

	// Validate object
	err = s.validate(obj, objectNamespace)
	if err != nil {
		return "", fmt.Errorf("failed to mutate object: %s", err.Error())
	}

	// Mutate object
	err = s.mutateObject(obj, objectNamespace)
	if err != nil {
		return "", fmt.Errorf("failed to mutate object: %s", err.Error())
	}

	// Create config-map
	err = s.createConfigMap(obj, objectNamespace)
	if err != nil {
		return "", fmt.Errorf("failed to create config-map: %s", err.Error())
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
