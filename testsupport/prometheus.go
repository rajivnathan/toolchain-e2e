package testsupport

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/stretchr/testify/require"
)

type PrometheusClient struct {
	client api.Client
}

func NewPrometheusClient(t *testing.T, url string) PrometheusClient {
	cl, err := api.NewClient(api.Config{
		Address: url,
	})
	require.NoError(t, err, "Error creating prometheus client")

	promClient := PrometheusClient{
		client: cl,
	}
	return promClient
}

func (c PrometheusClient) QueryRange(t *testing.T, query string, from, to time.Time) {
	v1api := v1.NewAPI(c.client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := v1.Range{
		Start: from,
		End:   to,
		Step:  time.Minute,
	}
	result, warnings, err := v1api.QueryRange(ctx, query, r)
	require.NoError(t, err, "Error querying Prometheus")
	if len(warnings) > 0 {
		fmt.Printf("Prometheus warnings: %v\n", warnings)
	}
	fmt.Printf("Query %s result:\n%v\n", query, result)
}
