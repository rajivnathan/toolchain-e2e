package testsupport

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	. "github.com/codeready-toolchain/toolchain-e2e/wait"
	"k8s.io/apimachinery/pkg/types"

	routev1 "github.com/openshift/api/route/v1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	monitoringNS       = "openshift-monitoring"
	clusterMetricsName = "prometheus-k8s"
)

func GetClusterMetricsRoute(t *testing.T, hostAwait *HostAwaitility) (routev1.Route, error) {
	clusterMetricsRoute := routev1.Route{}
	err := hostAwait.Client.Get(context.TODO(), types.NamespacedName{
		Namespace: monitoringNS,
		Name:      clusterMetricsName,
	}, &clusterMetricsRoute)
	return clusterMetricsRoute, err
}

func ClusterMetricsQuery(t *testing.T, host, query string) string {
	var req *http.Request
	client := http.Client{
		Timeout: time.Duration(1 * time.Second),
	}

	escapedQuery := url.QueryEscape(query)
	promQueryURL := fmt.Sprintf("https://%s/api/v1/query?query=%s", host, escapedQuery)

	fmt.Printf("cluster metrics query: '%s'\n", promQueryURL)
	req, err := http.NewRequest("GET", promQueryURL, nil)
	assert.NoError(t, err, "cluster metrics request failed")
	client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	f := framework.Global
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", f.KubeConfig.BearerToken))
	resp, err := client.Do(req)
	require.NoError(t, err, "Error querying cluster metrics")
	if err != nil {
		return "Error querying cluster metrics"
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	responseBody := buf.String()

	return responseBody
}

func formatTime(t time.Time) string {
	return strconv.FormatFloat(float64(t.Unix())+float64(t.Nanosecond())/1e9, 'f', -1, 64)
}
