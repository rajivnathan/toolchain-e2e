package run

import (
	"context"
	"testing"
	"time"

	perftest "github.com/codeready-toolchain/toolchain-e2e/perfapp/test"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func sampleRun(id, host string, phase Phase) *TestRun {
	return &TestRun{
		ID:        id,
		CreatedAt: time.Date(2026, 9, 17, 14, 30, 0, 0, time.UTC),
		CreatedBy: "alice",
		APIServer: host,
		Phase:     phase,
		SetupRuns: []SetupRun{
			{Name: "1user", Users: 1, Default: 1, Username: "setup"},
		},
	}
}

func TestCreateAndGet(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	tr := sampleRun("tr-20260917-143000", "https://api.one.example.com:6443", PhasePrepare)
	kube := []byte("kubeconfig-bytes")

	// when
	require.NoError(t, Create(context.TODO(), cl, "app", tr, kube))

	// then
	got, cm, err := Get(context.TODO(), cl, "app", tr.ID)
	require.NoError(t, err)
	require.Equal(t, tr.ID, got.ID)
	require.Equal(t, AppLabelValue, cm.Labels[LabelApp])
	require.Equal(t, tr.ID, cm.Labels[LabelTestRun])
	require.Equal(t, HostLabel(tr.APIServer), cm.Labels[LabelTestHost])

	gotKube, err := GetKubeconfig(context.TODO(), cl, "app", tr.ID)
	require.NoError(t, err)
	require.Equal(t, kube, gotKube)
}

func TestOverlapSameHostNonTerminal(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	host := "https://API.one.example.com:6443/"
	tr := sampleRun("tr-1", host, PhaseSetupRunning)
	require.NoError(t, Create(context.TODO(), cl, "app", tr, []byte("k")))

	// when
	id, err := FindOverlap(context.TODO(), cl, "app", "https://api.one.example.com:6443")

	// then
	require.NoError(t, err)
	require.Equal(t, "tr-1", id)
}

func TestOverlapDifferentHostAllowed(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	tr := sampleRun("tr-1", "https://api.one.example.com:6443", PhaseSetupRunning)
	require.NoError(t, Create(context.TODO(), cl, "app", tr, []byte("k")))

	// when
	id, err := FindOverlap(context.TODO(), cl, "app", "https://api.two.example.com:6443")

	// then
	require.NoError(t, err)
	require.Empty(t, id)
}

func TestOverlapTerminalSameHostAllowed(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	tr := sampleRun("tr-1", "https://api.one.example.com:6443", PhaseSucceeded)
	require.NoError(t, Create(context.TODO(), cl, "app", tr, []byte("k")))

	// when
	id, err := FindOverlap(context.TODO(), cl, "app", "https://api.one.example.com:6443")

	// then
	require.NoError(t, err)
	require.Empty(t, id)
}

func TestDeterministicJobNames(t *testing.T) {
	// given
	id := "tr-20260917-143000"

	// when
	deploy := DeployJobName(id)
	setup0 := SetupJobName(0, id)
	setup1 := SetupJobName(1, id)
	resultsCM := ResultsCMName(0)
	resultsKey := ResultsDataKey(1)

	// then
	require.Equal(t, "deploy-sandbox-tr-20260917-143000", deploy)
	require.Equal(t, "setup-0-tr-20260917-143000", setup0)
	require.Equal(t, "setup-1-tr-20260917-143000", setup1)
	require.Equal(t, "setup-run-results-0", resultsCM)
	require.Equal(t, "results-1.csv", resultsKey)
}

func TestHasResultsCSV(t *testing.T) {
	t.Run("status only", func(t *testing.T) {
		// given
		cm := &corev1.ConfigMap{Data: map[string]string{StatusKey: "{}"}}

		// when
		got := HasResultsCSV(cm)

		// then
		require.False(t, got)
	})
	t.Run("results key present", func(t *testing.T) {
		// given
		cm := &corev1.ConfigMap{Data: map[string]string{"results-0.csv": "a,b"}}

		// when
		got := HasResultsCSV(cm)

		// then
		require.True(t, got)
	})
}

func TestPutCSV(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	tr := sampleRun("tr-1", "https://api.one.example.com:6443", PhaseSetupRunning)
	require.NoError(t, Create(context.TODO(), cl, "app", tr, []byte("k")))

	// when
	require.NoError(t, PutCSV(context.TODO(), cl, "app", tr.ID, 0, "Item,Value\n"))

	// then
	cm := &corev1.ConfigMap{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{Namespace: "app", Name: ConfigMapName(tr.ID)}, cm))
	require.Equal(t, "Item,Value\n", cm.Data[ResultsDataKey(0)])
}

func TestHostLabelFitsKubernetes(t *testing.T) {
	// given
	apiServer := "https://api.example.com:6443"

	// when
	h := HostLabel(apiServer)

	// then
	require.LessOrEqual(t, len(h), 63)
	require.Equal(t, h, HostLabel("https://API.example.com:6443/"))
}

func TestUpdateStatusPreservesCSV(t *testing.T) {
	// given
	cl := perftest.NewFakeClient(t)
	tr := sampleRun("tr-1", "https://api.one.example.com:6443", PhaseSetupRunning)
	require.NoError(t, Create(context.TODO(), cl, "app", tr, []byte("k")))
	require.NoError(t, PutCSV(context.TODO(), cl, "app", tr.ID, 0, "csv"))
	tr.Phase = PhaseSucceeded

	// when
	require.NoError(t, UpdateStatus(context.TODO(), cl, "app", tr))

	// then
	cm := &corev1.ConfigMap{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{Namespace: "app", Name: ConfigMapName(tr.ID)}, cm))
	require.Equal(t, "csv", cm.Data[ResultsDataKey(0)])
	got, err := Decode(cm)
	require.NoError(t, err)
	require.Equal(t, PhaseSucceeded, got.Phase)
}

func TestListSkipsUnlabeled(t *testing.T) {
	// given
	other := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "app"}}
	cl := perftest.NewFakeClient(t, other)
	tr := sampleRun("tr-1", "https://api.one.example.com:6443", PhasePrepare)
	require.NoError(t, Create(context.TODO(), cl, "app", tr, []byte("k")))

	// when
	list, err := List(context.TODO(), cl, "app")

	// then
	require.NoError(t, err)
	require.Len(t, list, 1)
}
