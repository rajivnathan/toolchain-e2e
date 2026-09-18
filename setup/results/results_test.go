package results

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	cfg "github.com/codeready-toolchain/toolchain-e2e/setup/configuration"
	"github.com/codeready-toolchain/toolchain-e2e/setup/terminal"
	"github.com/codeready-toolchain/toolchain-e2e/setup/test"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestOutputResultsOmitsConfigMapWhenNameEmpty(t *testing.T) {
	// given
	term := newTestTerminal()
	t.Chdir(t.TempDir())
	cfg.Init(term)

	cl := test.NewFakeClient(t)
	r := New(term, cl, "", "sandbox-perf-test")
	r.AddResults([][]string{
		{"Item", "Value"},
		{"Users", "1"},
	})

	// when
	r.OutputResults()

	// then
	fileBytes, err := os.ReadFile(cfg.ResultsFilepath())
	require.NoError(t, err)
	require.Contains(t, string(fileBytes), "Users,1")

	cms := &corev1.ConfigMapList{}
	require.NoError(t, cl.List(context.TODO(), cms))
	require.Empty(t, cms.Items)
}

func TestOutputResultsWritesConfigMapMatchingFile(t *testing.T) {
	// given
	term := newTestTerminal()
	t.Chdir(t.TempDir())
	cfg.Init(term)

	cl := test.NewFakeClient(t)
	r := New(term, cl, "setup-run-results-0", "sandbox-perf-test")
	r.AddResults([][]string{
		{"Item", "Value"},
		{"Users", "2000"},
	})

	// when
	r.OutputResults()

	// then
	fileBytes, err := os.ReadFile(cfg.ResultsFilepath())
	require.NoError(t, err)
	require.NotEmpty(t, fileBytes)

	cm := &corev1.ConfigMap{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{
		Namespace: "sandbox-perf-test",
		Name:      "setup-run-results-0",
	}, cm))
	require.Equal(t, string(fileBytes), cm.Data[ResultsCSVKey])
}

func TestOutputResultsUpdatesExistingConfigMap(t *testing.T) {
	// given
	term := newTestTerminal()
	t.Chdir(t.TempDir())
	cfg.Init(term)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "setup-run-results-1",
			Namespace: "sandbox-perf-test",
		},
		Data: map[string]string{
			"keep": "yes",
		},
	}
	cl := test.NewFakeClient(t, existing)
	r := New(term, cl, "setup-run-results-1", "sandbox-perf-test")
	r.AddResults([][]string{
		{"Item", "Value"},
	})

	// when
	r.OutputResults()

	// then
	cm := &corev1.ConfigMap{}
	require.NoError(t, cl.Get(context.TODO(), types.NamespacedName{
		Namespace: "sandbox-perf-test",
		Name:      "setup-run-results-1",
	}, cm))
	require.Equal(t, "yes", cm.Data["keep"])
	fileBytes, err := os.ReadFile(cfg.ResultsFilepath())
	require.NoError(t, err)
	require.Equal(t, string(fileBytes), cm.Data[ResultsCSVKey])
}

func newTestTerminal() terminal.Terminal {
	return terminal.New(func() io.Reader { return strings.NewReader("") }, func() io.Writer { return io.Discard }, false)
}
