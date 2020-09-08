package perf

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	. "github.com/codeready-toolchain/toolchain-e2e/testsupport"
	. "github.com/codeready-toolchain/toolchain-e2e/wait"
	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestPerformance(t *testing.T) {
	// given
	logger, out, err := initLogger()
	require.NoError(t, err)
	defer out.Close()
	ctx, hostAwait, memberAwait := WaitForDeployments(t, &toolchainv1alpha1.UserSignupList{})
	defer ctx.Cleanup()

	// host metrics should become available at this point
	metricsService, err := hostAwait.WaitForMetricsService("host-operator-metrics")
	require.NoError(t, err, "failed while waiting for the 'host-operator-metrics' service")

	count := 1000
	t.Run(fmt.Sprintf("%d users", count), func(t *testing.T) {

		// trigger different path for user deactivation controller
		configMap := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "start-user-deactivation",
				Namespace: hostAwait.Namespace,
			},
			Data: map[string]string{
				"test-test": "test-test",
			},
		}
		err = hostAwait.FrameworkClient.Create(context.TODO(), configMap, CleanupOptions(ctx))
		require.NoError(t, err)

		testStart := time.Now()
		// given
		users := CreateMultipleSignups(t, ctx, hostAwait, memberAwait, count)
		for _, user := range users {
			_, err := hostAwait.WaitForMasterUserRecord(user.Spec.Username, UntilMasterUserRecordHasCondition(Provisioned()))
			require.NoError(t, err)
		}

		// update nstemplatetier deactivation timeout to trigger user deactivation after operator pod restarts
		// err := updateDeactivationTimeoutForTierTo(t, hostAwait, 1)
		// require.NoError(t, err, "error updating deactivation timeout for nstemplatetier")

		// // reset deactivation timeout when test is complete
		// defer updateDeactivationTimeoutForTierTo(t, hostAwait, 0)
		prometheusNS := "openshift-monitoring"
		prometheusName := "prometheus-k8s"

		prometheusRoute := routev1.Route{}
		if err := hostAwait.Client.Get(context.TODO(), types.NamespacedName{
			Namespace: prometheusNS,
			Name:      prometheusName,
		}, &prometheusRoute); err != nil {
			assert.NoError(t, err, "prometheus not ready")
			return
		}

		for i := 0; i < 1000; i++ {
			fmt.Printf("Pod restarts: %d\n", i+1)
			metricsRoute, err := restartPodAndPrintReconciles(logger, t, hostAwait, metricsService, count)
			if i%100 == 0 && err == nil {
				midTestTime := time.Now()
				deactivationTotalReconciles, err := GetCounter(metricsRoute.Status.Ingress[0].Host, "controller_runtime_reconcile_total", "controller", "deactivation-controller")
				require.NoError(t, err, "failed to get the total number of reconciles for the deactivation controller")
				printPerformance(t, testStart, midTestTime, prometheusRoute.Status.Ingress[0].Host, deactivationTotalReconciles)
			}
		}

		testEnd := time.Now()

		metricsRoute, err := hostAwait.SetupRouteForService(metricsService, "/metrics")
		require.NoError(t, err, "failed while setting up or waiting for the route to the 'host-operator-metrics' service to be available")
		deactivationTotalReconciles, err := GetCounter(metricsRoute.Status.Ingress[0].Host, "controller_runtime_reconcile_total", "controller", "deactivation-controller")
		require.NoError(t, err, "failed to get the total number of reconciles for the deactivation controller")

		printPerformance(t, testStart, testEnd, prometheusRoute.Status.Ingress[0].Host, deactivationTotalReconciles)

	})

}

// initLogger initializes a logger which will write to `$(ARTIFACT_DIR)/perf-<YYYYMMDD-HHmmSS>.log` or `./tmp/perf-<YYYYMMDD-HHmmSS>.log` if no `ARTIFACT_DIR`
// env var is defined.
// Notes:
// - the target directory will be created on-the-fly if needed
// - it's up to the caller to close the returned file at the end of the tests
func initLogger() (logr.Logger, *os.File, error) {
	// log messages that need to be retained after the OpenShift CI job completion must be written in a file located in `${ARTIFACT_DIR}`
	var artifactDir string
	if artifactDir = os.Getenv("ARTIFACT_DIR"); artifactDir == "" {
		pwd, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		artifactDir = filepath.Join(pwd, "tmp")
	}
	if _, err := os.Open(artifactDir); os.IsNotExist(err) {
		// make sure that `./tmp` exists
		if err = os.MkdirAll(artifactDir, os.ModeDir+os.ModePerm); err != nil {
			return nil, nil, err
		}
	}

	out, err := os.Create(path.Join(artifactDir, fmt.Sprintf("perf-%s.log", time.Now().Format("20060102-030405"))))
	if err != nil {
		return nil, nil, err
	}
	logger := zap.New(zap.WriteTo(out))
	fmt.Printf("configured logger to write messages in '%s'\n", out.Name())
	return logger, out, nil
}

// func updateDeactivationTimeoutForTierTo(t test.T, hostAwait *HostAwaitility, timeout int) error {
// 	basicTier := &toolchainv1alpha1.NSTemplateTier{}
// 	err := hostAwait.FrameworkClient.Get(context.TODO(), types.NamespacedName{Name: "basic", Namespace: hostAwait.Namespace}, basicTier)
// 	if err != nil {
// 		return err
// 	}
// 	basicTier.Spec.DeactivationTimeoutDays = timeout

// 	err = hostAwait.FrameworkClient.Update(context.TODO(), basicTier)
// 	return err
// }

func printCounter(url string, family string, labelKey string, labelValue string) {
	murCount, err := GetCounter(url, family, labelKey, labelValue)
	if err == nil {
		fmt.Printf("%s reconcile total: %f\n", labelValue, murCount)
	}
}

func restartPodAndPrintReconciles(logger logr.Logger, t *testing.T, hostAwait *HostAwaitility, metricsService v1.Service, count int) (routev1.Route, error) {
	// when deleting the host-operator pod to emulate an operator restart during redeployment.
	err := hostAwait.DeletePods(client.MatchingLabels{"name": "host-operator"})

	// then check how much time it takes to restart and process all existing resources
	require.NoError(t, err)

	host := hostAwait
	host.Timeout = 30 * time.Minute
	// host metrics should become available again at this point
	metricsRoute, err := hostAwait.SetupRouteForService(metricsService, "/metrics")
	require.NoError(t, err, "failed while setting up or waiting for the route to the 'host-operator-metrics' service to be available")

	metricsStart := time.Now()
	// measure time it takes to have an empty queue on the master-user-records
	// err = host.WaitUntilMetricsCounterHasValue(metricsRoute.Status.Ingress[0].Host, "workqueue_depth", "name", "usersignup-controller", 0)
	err = host.WaitUntilMetricsCounterHasValue(metricsRoute.Status.Ingress[0].Host, "controller_runtime_reconcile_total", "controller", "deactivation-controller", float64(count))
	assert.NoError(t, err, "failed to reach the expected queue depth")

	// deactivationTotalReconciles, err := GetCounter(metricsRoute.Status.Ingress[0].Host, "controller_runtime_reconcile_total", "controller", "deactivation-controller")
	// require.NoError(t, err, "failed to get the total number of reconciles for the deactivation controller")

	processedAllMurs := time.Now()
	logger.Info("done processing resources", "provisioned_users_count", count, "usersignup_processing_duration_ms", processedAllMurs.Sub(metricsStart).Milliseconds())
	fmt.Printf("usersignup_processing_duration_ms: %d\n", processedAllMurs.Sub(metricsStart).Milliseconds())

	return metricsRoute, err
}

func printPerformance(t *testing.T, testStart, testEnd time.Time, prometheusURL string, deactivationTotalReconciles float64) {
	testDuration := testEnd.Sub(testStart)
	testDurationSeconds := int(testDuration.Seconds())
	fmt.Printf("========================================================================\n")
	// fmt.Printf("Deactivation duration: %ds\n", int(testEnd.Sub(deactivationStart).Seconds()))
	fmt.Printf("Deactivation total reconciles: %f\n", deactivationTotalReconciles)
	fmt.Printf("Total duration: %ds\n", testDurationSeconds)
	fmt.Printf("===========================CPU Utilisation==============================\n")
	cpuAvgQuery := fmt.Sprintf(`1-avg(rate(node_cpu_seconds_total{mode="idle"}[%ds]))`, testDurationSeconds)
	cpuAvgResult := PrometheusQuery(t, prometheusURL, cpuAvgQuery)

	cpuMaxQuery := fmt.Sprintf(`1-min(rate(node_cpu_seconds_total{mode="idle"}[%ds]))`, testDurationSeconds)
	cpuMaxResult := PrometheusQuery(t, prometheusURL, cpuMaxQuery)

	cpuMinQuery := fmt.Sprintf(`1-max(rate(node_cpu_seconds_total{mode="idle"}[%ds]))`, testDurationSeconds)
	cpuMinResult := PrometheusQuery(t, prometheusURL, cpuMinQuery)
	fmt.Printf("Max: %s\nAvg: %s\nMin: %s\n", cpuMaxResult, cpuAvgResult, cpuMinResult)

	fmt.Printf("=========================Memory Utilisation============================\n")

	memoryAvgQuery := fmt.Sprintf(`1-avg_over_time(:node_memory_MemAvailable_bytes:sum[%ds])/sum(kube_node_status_allocatable_memory_bytes)`, testDurationSeconds)
	memoryAvgResult := PrometheusQuery(t, prometheusURL, memoryAvgQuery)

	memoryMaxQuery := fmt.Sprintf(`1-min_over_time(:node_memory_MemAvailable_bytes:sum[%ds])/sum(kube_node_status_allocatable_memory_bytes)`, testDurationSeconds)
	memoryMaxResult := PrometheusQuery(t, prometheusURL, memoryMaxQuery)

	memoryMinQuery := fmt.Sprintf(`1-max_over_time(:node_memory_MemAvailable_bytes:sum[%ds])/sum(kube_node_status_allocatable_memory_bytes)`, testDurationSeconds)
	memoryMinResult := PrometheusQuery(t, prometheusURL, memoryMinQuery)

	fmt.Printf("Max: %s\nAvg: %s\nMin: %s\n", memoryMaxResult, memoryAvgResult, memoryMinResult)
}
