package sidecar

import (
	"context"
	"fmt"
	"os"

	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SidecarInjector knows how to mark Kubernetes resources.
type SidecarInjector interface {
	Inject(ctx context.Context, obj metav1.Object) error
}

// NewSidecarInjector returns a new sidecar-injector that will inject sidecars.
func NewSidecarInjector() SidecarInjector {
	return sidecarinjector{}
}

type sidecarinjector struct{}

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
		Time:      "3600",
		Name:      "",
	}
	return &jobInfo
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

func (s sidecarinjector) getJobInformation(obj metav1.Object) JobInformation {
	var jobInfo JobInformation

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

	return jobInfo
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
	// Construct sbatch URL
	slurmWebhookURL := s.getSlurmWebhookURL()
	jobInfo := s.getJobInformation(obj)
	sbatchURL := s.constructSbatchURL(slurmWebhookURL, jobInfo)

	// Add init-container
	initContainer := corev1.Container{
		Name:    "initial-container",
		Image:   "curlimages/curl:7.75.0",
		Command: []string{"/bin/sh", "-c"},
		Args: []string{
			fmt.Sprintf("slurmWebhookURL=%s && sbatchURL=%s &&", slurmWebhookURL, sbatchURL) +
				"jobid=$(curl -s ${sbatchURL}) && " +
				"while true; " +
				"do " +
				"sleep 1; " +
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
	volume := corev1.Volume{
		Name: "k8s-slurm-injector-shared",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}

	switch v := obj.(type) {
	case *corev1.Pod:
		v.Spec.InitContainers = append([]corev1.Container{initContainer}, v.Spec.InitContainers...)
		v.Spec.Volumes = append([]corev1.Volume{volume}, v.Spec.Volumes...)
	case *batchv1.Job:
		v.Spec.Template.Spec.InitContainers =
			append([]corev1.Container{initContainer}, v.Spec.Template.Spec.InitContainers...)
		v.Spec.Template.Spec.Volumes = append([]corev1.Volume{volume}, v.Spec.Template.Spec.Volumes...)
	case *batchv1beta1.CronJob:
		v.Spec.JobTemplate.Spec.Template.Spec.InitContainers =
			append([]corev1.Container{initContainer}, v.Spec.JobTemplate.Spec.Template.Spec.InitContainers...)
		v.Spec.JobTemplate.Spec.Template.Spec.Volumes =
			append([]corev1.Volume{volume}, v.Spec.JobTemplate.Spec.Template.Spec.Volumes...)
	default:
		return fmt.Errorf("unsupported type")
	}

	// Modify annotations
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["k8s-slurm-injector/status"] = "injected"
	obj.SetAnnotations(annotations)

	return nil
}

func (s sidecarinjector) Inject(_ context.Context, obj metav1.Object) error {
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

	// Mutate object
	err := s.mutateObject(obj)
	if err != nil {
		return fmt.Errorf("failed to mutate object: %s", err.Error())
	}

	return nil
}

// DummyMarker is a marker that doesn't do anything.
var DummySidecarInjector SidecarInjector = dummySidecarInjector(0)

type dummySidecarInjector int

func (dummySidecarInjector) Inject(_ context.Context, _ metav1.Object) error { return nil }
