package metrics

import (
	"testing"

	"github.com/codeready-toolchain/toolchain-e2e/setup/test"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestGetPrometheusEndpointRouteByDefault(t *testing.T) {
	// given
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PrometheusRouteName,
			Namespace: OpenshiftMonitoringNS,
		},
		Spec: routev1.RouteSpec{
			Host: "prometheus-k8s-openshift-monitoring.apps.example.com",
		},
	}
	cl := test.NewFakeClient(t, route)

	// when
	url, err := getPrometheusEndpoint(cl, false)

	// then
	require.NoError(t, err)
	require.Equal(t, "https://prometheus-k8s-openshift-monitoring.apps.example.com", url)
}

func TestGetPrometheusEndpointInClusterUsesThanosService(t *testing.T) {
	// given
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ThanosQuerierServiceName,
			Namespace: OpenshiftMonitoringNS,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "tenancy", Port: 9092, TargetPort: intstr.FromInt(9092)},
				{Name: "web", Port: 9091, TargetPort: intstr.FromInt(9091)},
			},
		},
	}
	cl := test.NewFakeClient(t, svc)

	// when
	url, err := getPrometheusEndpoint(cl, true)

	// then
	require.NoError(t, err)
	require.Equal(t, "https://thanos-querier.openshift-monitoring.svc:9091", url)
}

func TestGetPrometheusEndpointInClusterUsesFirstPortWithoutWeb(t *testing.T) {
	// given
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ThanosQuerierServiceName,
			Namespace: OpenshiftMonitoringNS,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "metrics", Port: 9091, TargetPort: intstr.FromInt(9091)},
			},
		},
	}
	cl := test.NewFakeClient(t, svc)

	// when
	url, err := getPrometheusEndpoint(cl, true)

	// then
	require.NoError(t, err)
	require.Equal(t, "https://thanos-querier.openshift-monitoring.svc:9091", url)
}

func TestGetPrometheusEndpointInClusterMissingService(t *testing.T) {
	// given
	cl := test.NewFakeClient(t)

	// when
	_, err := getPrometheusEndpoint(cl, true)

	// then
	require.Error(t, err)
}

func TestGetPrometheusEndpointInClusterNoPorts(t *testing.T) {
	// given
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ThanosQuerierServiceName,
			Namespace: OpenshiftMonitoringNS,
		},
	}
	cl := test.NewFakeClient(t, svc)

	// when
	_, err := getPrometheusEndpoint(cl, true)

	// then
	require.ErrorContains(t, err, "has no ports")
}
