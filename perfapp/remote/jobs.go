package remote

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func RejectActiveJobs(ctx context.Context, cl client.Client) error {
	list := &batchv1.JobList{}
	err := cl.List(ctx, list, client.InNamespace(run.TestNamespace), client.MatchingLabels{run.LabelApp: run.JobAppValue})
	if err != nil {
		return err
	}
	for i := range list.Items {
		if !JobTerminal(&list.Items[i]) {
			return fmt.Errorf("test cluster already has an active setup Job %s", list.Items[i].Name)
		}
	}
	return nil
}

func GetJob(ctx context.Context, cl client.Client, name string) (*batchv1.Job, error) {
	job := &batchv1.Job{}
	err := cl.Get(ctx, types.NamespacedName{Namespace: run.TestNamespace, Name: name}, job)
	if err != nil {
		return nil, err
	}
	return job, nil
}

func EnsureJob(ctx context.Context, cl client.Client, job *batchv1.Job) (*batchv1.Job, bool, error) {
	existing, err := GetJob(ctx, cl, job.Name)
	if err == nil {
		return existing, false, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, false, err
	}
	if err := cl.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			existing, getErr := GetJob(ctx, cl, job.Name)
			return existing, false, getErr
		}
		return nil, false, err
	}
	return job, true, nil
}

func JobTerminal(job *batchv1.Job) bool {
	return JobSucceeded(job) || JobFailed(job)
}

func JobSucceeded(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func JobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func JobMessage(job *batchv1.Job) string {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobFailed || c.Type == batchv1.JobComplete) && c.Status == corev1.ConditionTrue && c.Message != "" {
			return c.Message
		}
	}
	return ""
}

func DeploySandboxJob(tr *run.TestRun) *batchv1.Job {
	return phaseJob(tr, run.DeployJobName(tr.ID), run.PhaseDeploySandbox, -1, []string{"deploy-sandbox"}, false, jobResources(0, true))
}

func SetupJob(tr *run.TestRun, index int) *batchv1.Job {
	sr := tr.SetupRuns[index]
	args := setupArgs(tr, index, sr)
	mount := sr.Custom > 0 && tr.TemplateFile != ""
	return phaseJob(tr, run.SetupJobName(index, tr.ID), run.PhaseSetupRunning, index, args, mount, jobResources(sr.Users, false))
}

func setupArgs(tr *run.TestRun, index int, sr run.SetupRun) []string {
	args := []string{
		"setup",
		"--users", strconv.Itoa(sr.Users),
		"--default", strconv.Itoa(sr.Default),
		"--custom", strconv.Itoa(sr.Custom),
		"--username", sr.Username,
		"--interactive=false",
		"--results-configmap", run.ResultsCMName(index),
		"--in-cluster-metrics",
	}
	if sr.Custom > 0 && tr.TemplateFile != "" {
		args = append(args, "--template", path.Join("/templates", tr.TemplateFile))
	}
	if len(tr.Workloads) > 0 {
		args = append(args, "--workloads", strings.Join(tr.Workloads, ","))
	}
	testname := tr.Testname
	if sr.Testname != "" {
		testname = sr.Testname
	}
	if testname != "" {
		args = append(args, "--testname", testname)
	}
	return args
}

func phaseJob(tr *run.TestRun, name string, phase run.Phase, index int, args []string, mountTemplate bool, resources corev1.ResourceRequirements) *batchv1.Job {
	labels := map[string]string{
		run.LabelApp:     run.JobAppValue,
		run.LabelTestRun: tr.ID,
		run.LabelPhase:   string(phase),
	}
	if index >= 0 {
		labels[run.LabelSetupIdx] = strconv.Itoa(index)
	}
	container := corev1.Container{
		Name:            "job",
		Image:           run.JobImage(tr.ImageTag),
		Args:            args,
		Resources:       resources,
		ImagePullPolicy: corev1.PullIfNotPresent,
	}
	podSpec := corev1.PodSpec{
		RestartPolicy:      corev1.RestartPolicyNever,
		ServiceAccountName: run.RunnerSA,
		Containers:         []corev1.Container{container},
	}
	if mountTemplate {
		podSpec.Volumes = []corev1.Volume{{
			Name: "templates",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: run.TemplatesCMName},
				},
			},
		}}
		podSpec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
			Name:      "templates",
			MountPath: "/templates",
			ReadOnly:  true,
		}}
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: run.TestNamespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: int32ptr(0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}

func int32ptr(v int32) *int32 { return &v }

func jobResources(users int, deploySandbox bool) corev1.ResourceRequirements {
	if deploySandbox || users < 1000 {
		return corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		}
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
	}
}
