package remote

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"
	perftest "github.com/codeready-toolchain/toolchain-e2e/perfapp/test"

	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

func init() {
	imageStreamReadyTimeout = 0
}

func TestPrepareRecordsBuildName(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	tr := &run.TestRun{ID: "tr-1", Steps: run.Pipeline([]run.Step{{Users: 1, Username: "setup"}})}

	// when
	err := Prepare(context.TODO(), cl, perftest.NewFakeInstantiator(cl), tr, nil, "")

	// then
	require.NoError(t, err)
	require.Equal(t, "perf-job-1", tr.BuildName)
	require.Equal(t, "tr-1", tr.ImageTag)

	b := &buildv1.Build{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{Namespace: run.TestNamespace, Name: tr.BuildName}, b))
	require.Equal(t, "perf-job:tr-1", b.Spec.Output.To.Name)
	require.Equal(t, "tr-1", b.Labels[run.LabelTestRun])

	bc := &buildv1.BuildConfig{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{Namespace: run.TestNamespace, Name: run.BuildConfigName}, bc))
	require.Equal(t, "perf-job:tr-1", bc.Spec.Output.To.Name)
	require.Equal(t, run.GitURI, bc.Spec.Source.Git.URI)
	require.Equal(t, run.GitRef, bc.Spec.Source.Git.Ref)

	is := &imagev1.ImageStream{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{Namespace: run.TestNamespace, Name: run.ImageStreamName}, is))

	ns := &corev1.Namespace{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{Name: run.TestNamespace}, ns))
}

func TestPrepareUsesGitOverride(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:     "tr-fork",
		GitURI: "https://github.com/rajivnathan/toolchain-e2e",
		GitRef: "my-branch",
		Steps:  run.Pipeline([]run.Step{{Users: 1, Username: "setup"}}),
	}

	// when
	err := Prepare(context.TODO(), cl, perftest.NewFakeInstantiator(cl), tr, nil, "")

	// then
	require.NoError(t, err)
	bc := &buildv1.BuildConfig{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{Namespace: run.TestNamespace, Name: run.BuildConfigName}, bc))
	require.Equal(t, "https://github.com/rajivnathan/toolchain-e2e", bc.Spec.Source.Git.URI)
	require.Equal(t, "my-branch", bc.Spec.Source.Git.Ref)
}

func TestStartBuildSendsInstantiateRequest(t *testing.T) {
	// given
	inst := &stubInstantiator{build: &buildv1.Build{ObjectMeta: metav1.ObjectMeta{Name: "perf-job-3"}}}

	// when
	b, err := StartBuild(context.TODO(), inst, &run.TestRun{ID: "tr-2", ImageTag: "tr-2"})

	// then
	require.NoError(t, err)
	require.Equal(t, "perf-job-3", b.Name)
	require.Equal(t, run.TestNamespace, inst.namespace)
	require.Equal(t, run.BuildConfigName, inst.name)
	require.Equal(t, run.BuildConfigName, inst.req.Name)
	require.Equal(t, "tr-2", inst.req.Labels[run.LabelTestRun])
	require.Equal(t, "BuildRequest", inst.req.Kind)
}

func TestNewInstantiatorRequiresConfig(t *testing.T) {
	// given
	var cfg *rest.Config

	// when
	_, err := NewInstantiator(cfg)

	// then
	require.ErrorContains(t, err, "rest config is required")
}

func TestInstantiatePostsBuildRequest(t *testing.T) {
	// given
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"apiVersion":"build.openshift.io/v1","kind":"Build","metadata":{"name":"perf-job-1"}}`))
	}))
	t.Cleanup(srv.Close)
	inst, err := NewInstantiator(&rest.Config{Host: srv.URL, APIPath: "/apis"})
	require.NoError(t, err)

	// when
	b, err := inst.Instantiate(context.TODO(), run.TestNamespace, run.BuildConfigName, &buildv1.BuildRequest{
		TypeMeta:   metav1.TypeMeta{APIVersion: buildv1.GroupVersion.String(), Kind: "BuildRequest"},
		ObjectMeta: metav1.ObjectMeta{Name: run.BuildConfigName},
	})

	// then
	require.NoError(t, err)
	require.Equal(t, "perf-job-1", b.Name)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/apis/build.openshift.io/v1/namespaces/sandbox-perf-test/buildconfigs/perf-job/instantiate", gotPath)
}

type stubInstantiator struct {
	namespace, name string
	req             *buildv1.BuildRequest
	build           *buildv1.Build
	err             error
}

func (s *stubInstantiator) Instantiate(_ context.Context, namespace, name string, request *buildv1.BuildRequest) (*buildv1.Build, error) {
	s.namespace = namespace
	s.name = name
	s.req = request
	return s.build, s.err
}

func TestPrepareReusesExistingImageStream(t *testing.T) {
	// given
	existing := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Name: run.ImageStreamName, Namespace: run.TestNamespace},
	}
	cl := perftest.NewFakeClient(t, existing)
	tr := &run.TestRun{ID: "tr-2", Steps: run.Pipeline([]run.Step{{Users: 1, Username: "setup"}})}

	// when
	err := Prepare(context.TODO(), cl, perftest.NewFakeInstantiator(cl), tr, nil, "")

	// then
	require.NoError(t, err)
	is := &imagev1.ImageStream{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{Namespace: run.TestNamespace, Name: run.ImageStreamName}, is))
}

func TestWaitForImageStreamRepository(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		// given
		imageStreamReadyTimeout = 2 * time.Second
		t.Cleanup(func() { imageStreamReadyTimeout = 0 })
		is := &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: run.ImageStreamName, Namespace: run.TestNamespace},
			Status:     imagev1.ImageStreamStatus{DockerImageRepository: "image-registry.openshift-image-registry.svc:5000/sandbox-perf-test/perf-job"},
		}
		cl := perftest.NewFakeClient(t, is)

		// when
		err := waitForImageStreamRepository(context.TODO(), cl)

		// then
		require.NoError(t, err)
	})

	t.Run("timeout", func(t *testing.T) {
		// given
		cl := perftest.NewFakeClient(t)
		require.NoError(t, EnsureImageStream(context.TODO(), cl))
		imageStreamReadyTimeout = 200 * time.Millisecond
		t.Cleanup(func() { imageStreamReadyTimeout = 0 })

		// when
		err := waitForImageStreamRepository(context.TODO(), cl)

		// then
		require.ErrorContains(t, err, "dockerImageRepository")
	})
}

func TestPrepareStartBuildFailure(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	inst := &stubInstantiator{err: fmt.Errorf("cannot start build")}
	tr := &run.TestRun{ID: "tr-1", ImageTag: "tr-1"}

	// when
	err := Prepare(context.TODO(), cl, inst, tr, nil, "")

	// then
	require.ErrorContains(t, err, "cannot start build")
	require.Empty(t, tr.BuildName)
}

func TestPrepareRejectsActiveJobs(t *testing.T) {
	// given
	active := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "setup-0-other",
			Namespace: run.TestNamespace,
			Labels:    map[string]string{run.LabelApp: run.JobAppValue},
		},
	}
	cl := perftest.NewFakeClient(t, active)
	tr := &run.TestRun{ID: "tr-1", ImageTag: "tr-1"}

	// when
	err := Prepare(context.TODO(), cl, perftest.NewFakeInstantiator(cl), tr, nil, "")

	// then
	require.ErrorContains(t, err, "already has an active setup Job")
}

func TestStepJobArgs(t *testing.T) {
	// given
	tr := &run.TestRun{
		ID:           "tr-1",
		ImageTag:     "tr-1",
		Workloads:    []string{"ns:name"},
		TemplateFile: "onboarding.yaml",
		Steps: run.Pipeline([]run.Step{
			{Users: 1, Default: 1, Custom: 0, Username: "setup"},
			{Users: 2000, Default: 2000, Custom: 2000, Username: "cupcake"},
		}),
	}

	t.Run("deploy sandbox", func(t *testing.T) {
		// when
		job, err := StepJob(tr, 0)

		// then
		require.NoError(t, err)
		require.Equal(t, []string{"deploy-sandbox"}, job.Spec.Template.Spec.Containers[0].Args)
		require.Equal(t, string(run.StepDeploySandbox), job.Labels[run.LabelPhase])
		require.Equal(t, "0", job.Labels[run.LabelStepIndex])
	})

	t.Run("no template when custom is 0", func(t *testing.T) {
		// when
		job, err := StepJob(tr, 1)

		// then
		require.NoError(t, err)
		require.Contains(t, job.Spec.Template.Spec.Containers[0].Args, "--in-cluster-metrics")
		require.Contains(t, job.Spec.Template.Spec.Containers[0].Args, "setup-run-results-1")
		require.NotContains(t, job.Spec.Template.Spec.Containers[0].Args, "--template")
		require.Empty(t, job.Spec.Template.Spec.Volumes)
		require.Equal(t, "1", job.Labels[run.LabelStepIndex])
	})

	t.Run("template mount when custom is set", func(t *testing.T) {
		// when
		job, err := StepJob(tr, 2)

		// then
		require.NoError(t, err)
		require.Contains(t, job.Spec.Template.Spec.Containers[0].Args, "--template")
		require.Contains(t, job.Spec.Template.Spec.Containers[0].Args, "/templates/onboarding.yaml")
		require.NotEmpty(t, job.Spec.Template.Spec.Volumes)
		require.Equal(t, "4Gi", job.Spec.Template.Spec.Containers[0].Resources.Requests.Memory().String())
	})
}

func TestEnsureJobDoesNotDuplicate(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	require.NoError(t, EnsureNamespace(context.TODO(), cl))
	tr := &run.TestRun{ID: "tr-1", ImageTag: "tr-1", Steps: run.Pipeline(nil)}
	job, err := StepJob(tr, 0)
	require.NoError(t, err)

	// when
	got, created, err := EnsureJob(context.TODO(), cl, job)

	// then
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, job.Name, got.Name)

	// when
	again, err := StepJob(tr, 0)
	require.NoError(t, err)
	got, created, err = EnsureJob(context.TODO(), cl, again)

	// then
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, job.Name, got.Name)
	list := &batchv1.JobList{}
	require.NoError(t, cl.List(context.TODO(), list))
	require.Len(t, list.Items, 1)
}
