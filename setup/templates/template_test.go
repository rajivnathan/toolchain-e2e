package templates

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-e2e/setup/test"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestApplyObjectRetriesUntilKindIsRegistered(t *testing.T) {
	restore := shortenApplyRetry(t, time.Millisecond, time.Second)
	defer restore()

	cl := test.NewFakeClient(t)
	gets := 0
	cl.MockGet = func(ctx context.Context, key types.NamespacedName, obj runtimeclient.Object, opts ...runtimeclient.GetOption) error {
		gets++
		if gets <= 2 {
			return &meta.NoKindMatchError{
				GroupKind:        schema.GroupKind{Group: "opendatahub.io", Kind: "OdhDashboardConfig"},
				SearchedVersions: []string{"v1alpha"},
			}
		}
		return cl.Client.Get(ctx, key, obj, opts...)
	}
	cl.MockPatch = func(context.Context, runtimeclient.Object, runtimeclient.Patch, ...runtimeclient.PatchOption) error {
		return nil
	}

	err := applyObject(context.Background(), client.NewServerSideApplyClient(cl, fieldManager), dashboardConfig())

	require.NoError(t, err)
	require.Greater(t, gets, 2)
}

func TestApplyObjectStopsWhenKindIsNeverRegistered(t *testing.T) {
	restore := shortenApplyRetry(t, time.Millisecond, 20*time.Millisecond)
	defer restore()

	cl := test.NewFakeClient(t)
	cl.MockGet = func(context.Context, types.NamespacedName, runtimeclient.Object, ...runtimeclient.GetOption) error {
		return &meta.NoKindMatchError{
			GroupKind:        schema.GroupKind{Group: "opendatahub.io", Kind: "OdhDashboardConfig"},
			SearchedVersions: []string{"v1alpha"},
		}
	}

	err := applyObject(context.Background(), client.NewServerSideApplyClient(cl, fieldManager), dashboardConfig())

	require.ErrorContains(t, err, `could not apply resource 'odh-dashboard-config' in namespace 'redhat-ods-applications'`)
	require.ErrorContains(t, err, `no matches for kind "OdhDashboardConfig" in version "opendatahub.io/v1alpha"`)
}

func shortenApplyRetry(t *testing.T, interval, timeout time.Duration) func() {
	t.Helper()
	origInterval, origTimeout := applyRetryInterval, applyTimeout
	applyRetryInterval = interval
	applyTimeout = timeout
	return func() {
		applyRetryInterval = origInterval
		applyTimeout = origTimeout
	}
}

func dashboardConfig() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "opendatahub.io", Version: "v1alpha", Kind: "OdhDashboardConfig"})
	obj.SetName("odh-dashboard-config")
	obj.SetNamespace("redhat-ods-applications")
	return obj
}
