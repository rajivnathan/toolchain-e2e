package main

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/toolchain-e2e/perfapp/remote"
	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"
	perftest "github.com/codeready-toolchain/toolchain-e2e/perfapp/test"

	buildv1 "github.com/openshift/api/build/v1"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const appNS = "onboarding-perfapp"

func newPoller(t *testing.T, appCl, testCl client.Client) *Poller {
	t.Helper()
	return &Poller{
		AppClient: appCl,
		Namespace: appNS,
		NewTestClient: func(_ []byte) (client.Client, error) {
			return testCl, nil
		},
	}
}

func seedRun(t *testing.T, appCl client.Client, tr *run.TestRun) {
	t.Helper()
	require.NoError(t, run.Create(context.TODO(), appCl, appNS, tr, []byte("kube")))
}

func completeBuild(t *testing.T, cl client.Client, name string) {
	t.Helper()
	b := &buildv1.Build{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: run.TestNamespace},
	}
	require.NoError(t, cl.Create(context.TODO(), b))
	b.Status.Phase = buildv1.BuildPhaseComplete
	require.NoError(t, cl.Status().Update(context.TODO(), b))
}

func setJobCondition(t *testing.T, cl client.Client, name string, ctype batchv1.JobConditionType) {
	t.Helper()
	job := &batchv1.Job{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{Namespace: run.TestNamespace, Name: name}, job))
	job.Status.Conditions = []batchv1.JobCondition{{Type: ctype, Status: corev1.ConditionTrue, Message: string(ctype)}}
	require.NoError(t, cl.Status().Update(context.TODO(), job))
}

func mustStepJob(t *testing.T, tr *run.TestRun, index int) *batchv1.Job {
	t.Helper()
	job, err := remote.StepJob(tr, index)
	require.NoError(t, err)
	return job
}

func TestPollerPrepareCompleteCreatesFirstStepOnce(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:        "tr-1",
		Phase:     run.PhasePrepare,
		BuildName: "perf-job-tr-1",
		ImageTag:  "tr-1",
		APIServer: "https://api.one.example.com:6443",
		Steps: run.Pipeline([]run.Step{
			{Users: 1, Default: 1, Username: "setup"},
			{Users: 2, Default: 2, Username: "cupcake"},
		}),
	}
	seedRun(t, appCl, tr)
	completeBuild(t, testCl, tr.BuildName)
	p := newPoller(t, appCl, testCl)

	// when
	require.NoError(t, p.Advance(context.TODO(), tr))
	require.NoError(t, p.Advance(context.TODO(), reload(t, appCl, tr.ID)))

	// then
	list := &batchv1.JobList{}
	require.NoError(t, testCl.List(context.TODO(), list))
	require.Len(t, list.Items, 1)
	require.Equal(t, run.StepJobName(0, tr.ID), list.Items[0].Name)

	got := reload(t, appCl, tr.ID)
	require.Equal(t, run.PhaseRunning, got.Phase)
	require.Equal(t, 0, got.StepIndex)
	require.Equal(t, run.StepRunning, got.Steps[0].Status)
	require.Equal(t, string(buildv1.BuildPhaseComplete), got.BuildPhase)
}

func TestPollerPrepareRunningRecordsBuildStatus(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:        "tr-1",
		Phase:     run.PhasePrepare,
		BuildName: "perf-job-tr-1",
		ImageTag:  "tr-1",
		APIServer: "https://api.one.example.com:6443",
		Steps:     run.Pipeline([]run.Step{{Users: 1, Default: 1, Username: "setup"}}),
	}
	seedRun(t, appCl, tr)
	b := &buildv1.Build{ObjectMeta: metav1.ObjectMeta{Name: tr.BuildName, Namespace: run.TestNamespace}}
	require.NoError(t, testCl.Create(context.TODO(), b))
	b.Status.Phase = buildv1.BuildPhaseRunning
	b.Status.Message = "Fetching application source"
	require.NoError(t, testCl.Status().Update(context.TODO(), b))
	p := newPoller(t, appCl, testCl)

	// when
	require.NoError(t, p.Advance(context.TODO(), tr))

	// then
	got := reload(t, appCl, tr.ID)
	require.Equal(t, run.PhasePrepare, got.Phase)
	require.Equal(t, string(buildv1.BuildPhaseRunning), got.BuildPhase)
	require.Equal(t, "Fetching application source", got.BuildMessage)
	list := &batchv1.JobList{}
	require.NoError(t, testCl.List(context.TODO(), list))
	require.Empty(t, list.Items)
}

func TestPollerPrepareFailedRecordsBuildLog(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:        "tr-1",
		Phase:     run.PhasePrepare,
		BuildName: "perf-job-tr-1",
		ImageTag:  "tr-1",
		APIServer: "https://api.one.example.com:6443",
		Steps:     run.Pipeline([]run.Step{{Users: 1, Default: 1, Username: "setup"}}),
	}
	seedRun(t, appCl, tr)
	b := &buildv1.Build{ObjectMeta: metav1.ObjectMeta{Name: tr.BuildName, Namespace: run.TestNamespace}}
	require.NoError(t, testCl.Create(context.TODO(), b))
	b.Status.Phase = buildv1.BuildPhaseFailed
	b.Status.Message = "genericbuild failed due to error"
	b.Status.LogSnippet = "fatal: detected dubious ownership"
	require.NoError(t, testCl.Status().Update(context.TODO(), b))
	p := newPoller(t, appCl, testCl)

	// when
	require.NoError(t, p.Advance(context.TODO(), tr))

	// then
	got := reload(t, appCl, tr.ID)
	require.Equal(t, run.PhaseFailed, got.Phase)
	require.Equal(t, string(buildv1.BuildPhaseFailed), got.BuildPhase)
	require.Contains(t, got.LastError, "genericbuild failed")
	require.Contains(t, got.LastError, "dubious ownership")
	require.Equal(t, "fatal: detected dubious ownership", got.BuildLog)
}

func TestPollerDeploySuccessCreatesNextStepOnce(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:        "tr-1",
		Phase:     run.PhaseRunning,
		StepIndex: 0,
		BuildName: "perf-job-tr-1",
		ImageTag:  "tr-1",
		APIServer: "https://api.one.example.com:6443",
		Steps: []run.Step{
			{Kind: run.StepDeploySandbox, Name: "deploy-sandbox", Job: run.StepJobName(0, "tr-1"), Status: run.StepRunning},
			{Kind: run.StepSetup, Users: 1, Default: 1, Username: "setup"},
			{Kind: run.StepSetup, Users: 2, Default: 2, Username: "cupcake"},
		},
	}
	seedRun(t, appCl, tr)
	require.NoError(t, testCl.Create(context.TODO(), mustStepJob(t, tr, 0)))
	setJobCondition(t, testCl, run.StepJobName(0, tr.ID), batchv1.JobComplete)
	p := newPoller(t, appCl, testCl)

	// when
	require.NoError(t, p.Advance(context.TODO(), reload(t, appCl, tr.ID)))
	require.NoError(t, p.Advance(context.TODO(), reload(t, appCl, tr.ID)))

	// then
	got := reload(t, appCl, tr.ID)
	require.Equal(t, run.PhaseRunning, got.Phase)
	require.Equal(t, 1, got.StepIndex)
	require.Equal(t, run.StepSucceeded, got.Steps[0].Status)
	require.Equal(t, run.StepJobName(1, tr.ID), got.Steps[1].Job)
	require.Equal(t, run.StepRunning, got.Steps[1].Status)

	list := &batchv1.JobList{}
	require.NoError(t, testCl.List(context.TODO(), list, client.MatchingLabels{run.LabelStepIndex: "1"}))
	require.Len(t, list.Items, 1)
}

func TestPollerSetup0FailedDoesNotCreateSetup1(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:        "tr-1",
		Phase:     run.PhaseRunning,
		StepIndex: 1,
		ImageTag:  "tr-1",
		APIServer: "https://api.one.example.com:6443",
		Steps: []run.Step{
			{Kind: run.StepDeploySandbox, Job: run.StepJobName(0, "tr-1"), Status: run.StepSucceeded},
			{Kind: run.StepSetup, Users: 1, Default: 1, Username: "setup", Job: run.StepJobName(1, "tr-1"), Status: run.StepRunning},
			{Kind: run.StepSetup, Users: 2, Default: 2, Username: "cupcake"},
		},
	}
	seedRun(t, appCl, tr)
	require.NoError(t, testCl.Create(context.TODO(), mustStepJob(t, tr, 1)))
	setJobCondition(t, testCl, run.StepJobName(1, tr.ID), batchv1.JobFailed)
	p := newPoller(t, appCl, testCl)

	// when
	require.NoError(t, p.Advance(context.TODO(), reload(t, appCl, tr.ID)))

	// then
	got := reload(t, appCl, tr.ID)
	require.Equal(t, run.PhaseFailed, got.Phase)
	require.Equal(t, run.StepFailed, got.Steps[1].Status)
	require.Empty(t, got.Steps[2].Job)

	_, err := remote.GetJob(context.TODO(), testCl, run.StepJobName(2, tr.ID))
	require.True(t, client.IgnoreNotFound(err) == nil && err != nil)
}

func TestPollerLastSetupSuccess(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:        "tr-1",
		Phase:     run.PhaseRunning,
		StepIndex: 2,
		ImageTag:  "tr-1",
		APIServer: "https://api.one.example.com:6443",
		Steps: []run.Step{
			{Kind: run.StepDeploySandbox, Job: run.StepJobName(0, "tr-1"), Status: run.StepSucceeded},
			{Kind: run.StepSetup, Users: 1, Default: 1, Username: "setup", Job: run.StepJobName(1, "tr-1"), Status: run.StepSucceeded},
			{Kind: run.StepSetup, Users: 2, Default: 2, Username: "cupcake", Job: run.StepJobName(2, "tr-1"), Status: run.StepRunning},
		},
	}
	seedRun(t, appCl, tr)
	require.NoError(t, testCl.Create(context.TODO(), mustStepJob(t, tr, 2)))
	setJobCondition(t, testCl, run.StepJobName(2, tr.ID), batchv1.JobComplete)
	require.NoError(t, testCl.Create(context.TODO(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: run.ResultsCMName(2), Namespace: run.TestNamespace},
		Data:       map[string]string{run.ResultsCSVKey: "Item,Value\nUsers,2\n"},
	}))
	p := newPoller(t, appCl, testCl)

	// when
	require.NoError(t, p.Advance(context.TODO(), reload(t, appCl, tr.ID)))

	// then
	got, cm, err := run.Get(context.TODO(), appCl, appNS, tr.ID)
	require.NoError(t, err)
	require.Equal(t, run.PhaseSucceeded, got.Phase)
	require.Equal(t, run.StepSucceeded, got.Steps[2].Status)
	require.Equal(t, "Item,Value\nUsers,2\n", cm.Data[run.ResultsDataKey(2)])
}

func TestPollerCopyCSVOnSetupSuccess(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:        "tr-1",
		Phase:     run.PhaseRunning,
		StepIndex: 1,
		ImageTag:  "tr-1",
		APIServer: "https://api.one.example.com:6443",
		Steps: []run.Step{
			{Kind: run.StepDeploySandbox, Status: run.StepSucceeded},
			{Kind: run.StepSetup, Users: 1, Default: 1, Username: "setup", Job: run.StepJobName(1, "tr-1"), Status: run.StepRunning},
			{Kind: run.StepSetup, Users: 2, Default: 2, Username: "cupcake"},
		},
	}
	seedRun(t, appCl, tr)
	require.NoError(t, testCl.Create(context.TODO(), mustStepJob(t, tr, 1)))
	setJobCondition(t, testCl, run.StepJobName(1, tr.ID), batchv1.JobComplete)
	require.NoError(t, testCl.Create(context.TODO(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: run.ResultsCMName(1), Namespace: run.TestNamespace},
		Data:       map[string]string{run.ResultsCSVKey: "a,b\n"},
	}))
	p := newPoller(t, appCl, testCl)

	// when
	require.NoError(t, p.Advance(context.TODO(), reload(t, appCl, tr.ID)))

	// then
	got, cm, err := run.Get(context.TODO(), appCl, appNS, tr.ID)
	require.NoError(t, err)
	require.Equal(t, "a,b\n", cm.Data[run.ResultsDataKey(1)])
	require.Equal(t, run.PhaseRunning, got.Phase)
	require.Equal(t, 2, got.StepIndex)
	require.Equal(t, run.StepJobName(2, tr.ID), got.Steps[2].Job)
}

func TestPollerTeardownWithoutCSVFailed(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:        "tr-1",
		Phase:     run.PhaseRunning,
		APIServer: "https://api.one.example.com:6443",
		Steps:     run.Pipeline([]run.Step{{Users: 1, Default: 1, Username: "setup"}}),
	}
	require.NoError(t, run.Create(context.TODO(), appCl, appNS, tr, []byte("kube")))
	require.NoError(t, appCl.Delete(context.TODO(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: run.SecretName(tr.ID), Namespace: appNS},
	}))
	p := newPoller(t, appCl, perftest.NewFakeClient(t))

	// when
	require.NoError(t, p.Advance(context.TODO(), reload(t, appCl, tr.ID)))

	// then
	got := reload(t, appCl, tr.ID)
	require.Equal(t, run.PhaseFailed, got.Phase)
	require.Contains(t, got.LastError, "torn down")
}

func TestPollerSetupSuccessWithoutResultsFailed(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:        "tr-1",
		Phase:     run.PhaseRunning,
		StepIndex: 1,
		ImageTag:  "tr-1",
		APIServer: "https://api.one.example.com:6443",
		Steps: []run.Step{
			{Kind: run.StepDeploySandbox, Status: run.StepSucceeded},
			{Kind: run.StepSetup, Users: 1, Default: 1, Username: "setup", Job: run.StepJobName(1, "tr-1"), Status: run.StepRunning},
		},
	}
	seedRun(t, appCl, tr)
	require.NoError(t, testCl.Create(context.TODO(), mustStepJob(t, tr, 1)))
	setJobCondition(t, testCl, run.StepJobName(1, tr.ID), batchv1.JobComplete)
	p := newPoller(t, appCl, testCl)

	// when
	require.NoError(t, p.Advance(context.TODO(), reload(t, appCl, tr.ID)))

	// then
	got := reload(t, appCl, tr.ID)
	require.Equal(t, run.PhaseFailed, got.Phase)
	require.Contains(t, got.LastError, "results ConfigMap")
}

func reload(t *testing.T, cl client.Client, id string) *run.TestRun {
	t.Helper()
	tr, _, err := run.Get(context.TODO(), cl, appNS, id)
	require.NoError(t, err)
	return tr
}
