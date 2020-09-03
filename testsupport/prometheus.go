package testsupport

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func PrometheusQuery(t *testing.T, host, query string) string {
	var req *http.Request

	client := http.Client{
		Timeout: time.Duration(1 * time.Second),
	}

	escapedQuery := url.QueryEscape(query)
	// startParam := formatTime(start)
	// endParam := formatTime(end)
	// step := strconv.FormatFloat(1.0, 'f', -1, 64)

	promQueryURL := fmt.Sprintf("https://%s/api/v1/query?query=%s", host, escapedQuery)
	// promQueryURL := fmt.Sprintf("https://%s/api/v1/query_range?query=%s&start=%s&end=%s&step=%s", host, escapedQuery, startParam, endParam, step)

	fmt.Printf("prometheus query: '%s'\n", promQueryURL)
	req, err := http.NewRequest("GET", promQueryURL, nil)
	assert.NoError(t, err, "prometheus request failed")
	client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	f := framework.Global
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", f.KubeConfig.BearerToken))
	// fmt.Printf("Token: %s\n", f.KubeConfig.BearerToken)
	resp, err := client.Do(req)
	require.NoError(t, err, "Error querying prometheus")
	if err != nil {
		return "Error querying prometheus"
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	responseBody := buf.String()
	// fmt.Printf("Response: %+v, response body: %+v\n", resp, responseBody)

	return responseBody
}

func formatTime(t time.Time) string {
	return strconv.FormatFloat(float64(t.Unix())+float64(t.Nanosecond())/1e9, 'f', -1, 64)
}
