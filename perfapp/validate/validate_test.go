package validate

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"
	perftest "github.com/codeready-toolchain/toolchain-e2e/perfapp/test"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const validTemplate = `
kind: Template
apiVersion: template.openshift.io/v1
metadata:
  name: custom
objects: []
`

func fakeKubeconfig() []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://api.example.com:6443
    insecure-skip-tls-verify: true
  name: test
contexts:
- context:
    cluster: test
    user: admin
  name: test
current-context: test
users:
- name: admin
  user:
    token: fake
`)
}

func factory(t *testing.T, allowed bool) ClientFactory {
	cl := perftest.NewFakeClient(t)
	perftest.AllowSAR(cl, allowed)
	return func(kubeconfig []byte) (client.Client, *rest.Config, error) {
		cfg, err := RESTConfig(kubeconfig)
		if err != nil {
			return nil, nil, err
		}
		return cl, cfg, nil
	}
}

func TestSubmitSuccess(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
		SetupRuns: []run.SetupRun{
			{Name: "1user", Users: 1, Default: 1, Username: "setup"},
			{Name: "2k", Users: 2000, Default: 2000, Username: "cupcake"},
		},
	}

	// when
	got, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.NoError(t, err)
	require.Equal(t, "https://api.example.com:6443", got.APIServer)
	require.Len(t, got.SetupRuns, 2)
	require.Empty(t, got.TemplateName)
}

func TestBadKubeconfig(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: []byte("not-a-kubeconfig"),
		SetupRuns:  []run.SetupRun{{Users: 1, Default: 1, Username: "setup"}},
	}

	// when
	_, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.ErrorContains(t, err, "invalid kubeconfig")
}

func TestSARDeny(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
		SetupRuns:  []run.SetupRun{{Users: 1, Default: 1, Username: "setup"}},
	}

	// when
	_, err := Submit(context.TODO(), in, factory(t, false))

	// then
	require.ErrorContains(t, err, "cluster-admin")
}

func TestEmptySetupRuns(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
	}

	// when
	_, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.ErrorContains(t, err, "at least one setup run")
}

func TestDuplicateUsername(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
		SetupRuns: []run.SetupRun{
			{Users: 1, Default: 1, Username: "setup"},
			{Users: 2, Default: 2, Username: "setup"},
		},
	}

	// when
	_, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.ErrorContains(t, err, "already used")
}

func TestTransformUsernameReject(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
		SetupRuns:  []run.SetupRun{{Users: 1, Default: 1, Username: "openshiftuser"}},
	}

	// when
	_, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.ErrorContains(t, err, "would be transformed")
}

func TestCustomGreaterThanUsers(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
		SetupRuns:  []run.SetupRun{{Users: 1, Default: 1, Custom: 2, Username: "setup"}},
	}

	// when
	_, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.ErrorContains(t, err, "custom must be between")
}

func TestMissingTemplateWhenCustom(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
		SetupRuns:  []run.SetupRun{{Users: 2, Default: 0, Custom: 2, Username: "setup"}},
	}

	// when
	_, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.ErrorContains(t, err, "Template is required")
}

func TestNonTemplateYAML(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
		SetupRuns:  []run.SetupRun{{Users: 2, Default: 0, Custom: 2, Username: "setup"}},
		Template:   []byte("kind: ConfigMap\napiVersion: v1\nmetadata:\n  name: x\n"),
	}

	// when
	_, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.ErrorContains(t, err, "invalid template")
}

func TestAllCustomZeroWithoutTemplate(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
		SetupRuns:  []run.SetupRun{{Users: 1, Default: 1, Custom: 0, Username: "setup"}},
	}

	// when
	got, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.NoError(t, err)
	require.Empty(t, got.TemplateName)
}

func TestCustomWithValidTemplate(t *testing.T) {
	// given
	in := Input{
		Kubeconfig:   fakeKubeconfig(),
		SetupRuns:    []run.SetupRun{{Users: 2, Default: 0, Custom: 2, Username: "setup"}},
		Template:     []byte(validTemplate),
		TemplateName: "onboarding.yaml",
	}

	// when
	got, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.NoError(t, err)
	require.Equal(t, "onboarding.yaml", got.TemplateName)
}

func TestInvalidWorkload(t *testing.T) {
	// given
	in := Input{
		Kubeconfig: fakeKubeconfig(),
		SetupRuns:  []run.SetupRun{{Users: 1, Default: 1, Username: "setup"}},
		Workloads:  []string{"not-a-pair"},
	}

	// when
	_, err := Submit(context.TODO(), in, factory(t, true))

	// then
	require.ErrorContains(t, err, "namespace:name")
}
