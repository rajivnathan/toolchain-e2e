package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	commontest "github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/codeready-toolchain/toolchain-e2e/perfapp/remote"
	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"
	perftest "github.com/codeready-toolchain/toolchain-e2e/perfapp/test"
	"github.com/codeready-toolchain/toolchain-e2e/perfapp/validate"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func init() {
	remote.SetImageStreamReadyTimeout(0)
}

func newHTTPServer(t *testing.T, appCl, testCl *commontest.FakeClient) *Server {
	t.Helper()
	perftest.AllowSAR(testCl, true)
	srv, err := NewServer(appCl, appNS, func(kube []byte) (client.Client, *rest.Config, error) {
		cfg, err := validate.RESTConfig(kube)
		if err != nil {
			return nil, nil, err
		}
		return testCl, cfg, nil
	})
	require.NoError(t, err)
	srv.NewInstantiator = func(*rest.Config) (remote.Instantiator, error) {
		return perftest.NewFakeInstantiator(testCl), nil
	}
	srv.Now = func() time.Time { return time.Date(2026, 9, 17, 14, 30, 0, 0, time.UTC) }
	return srv
}

func TestGetFormHasREADMEPresets(t *testing.T) {
	// given
	srv := newHTTPServer(t, perftest.NewFakeClient(t), perftest.NewFakeClient(t))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// when
	srv.Handler().ServeHTTP(rec, req)

	// then
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	require.Contains(t, body, `value="1user"`)
	require.Contains(t, body, `value="cupcake"`)
	require.Contains(t, body, `value="2000"`)
	require.Contains(t, body, "onboarding operator must already be installed")
}

func TestPostRunValidationError(t *testing.T) {
	// given
	srv := newHTTPServer(t, perftest.NewFakeClient(t), perftest.NewFakeClient(t))
	body, ctype := multipartBody(t, map[string]string{
		"run.users":    "1",
		"run.default":  "1",
		"run.custom":   "0",
		"run.username": "setup",
		"run.name":     "1user",
	}, map[string]string{"kubeconfig": "not-a-kubeconfig"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/runs", body)
	req.Header.Set("Content-Type", ctype)

	// when
	srv.Handler().ServeHTTP(rec, req)

	// then
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "error=")
}

func TestPostRunOverlapConflict(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	existing := &run.TestRun{
		ID:        "tr-existing",
		Phase:     run.PhaseSetupRunning,
		APIServer: "https://api.example.com:6443",
		CreatedBy: "bob",
		SetupRuns: []run.SetupRun{{Users: 1, Default: 1, Username: "setup"}},
	}
	require.NoError(t, run.Create(context.TODO(), appCl, appNS, existing, []byte("k")))

	srv := newHTTPServer(t, appCl, perftest.NewFakeClient(t))
	body, ctype := multipartBody(t, map[string]string{
		"run.users":    "1",
		"run.default":  "1",
		"run.custom":   "0",
		"run.username": "setup",
		"run.name":     "1user",
	}, map[string]string{"kubeconfig": string(fakeKubeconfig("https://api.example.com:6443"))})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/runs", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("X-Forwarded-User", "alice")

	// when
	srv.Handler().ServeHTTP(rec, req)

	// then
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestCreateRunPrepareFailureSetsFailed(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	srv := newHTTPServer(t, appCl, testCl)
	inst := perftest.NewFakeInstantiator(testCl)
	inst.Err = fmt.Errorf("cannot start build")
	srv.NewInstantiator = func(*rest.Config) (remote.Instantiator, error) {
		return inst, nil
	}
	in := validate.Input{
		Kubeconfig: fakeKubeconfig("https://api.example.com:6443"),
		SetupRuns:  []run.SetupRun{{Users: 1, Default: 1, Username: "setup"}},
	}

	// when
	id, status, msg := srv.createRun(context.TODO(), "alice", in)

	// then
	require.Equal(t, http.StatusInternalServerError, status)
	require.Contains(t, msg, "cannot start build")
	require.NotEmpty(t, id)

	got, _, err := run.Get(context.TODO(), appCl, appNS, id)
	require.NoError(t, err)
	require.Equal(t, run.PhaseFailed, got.Phase)
	require.Contains(t, got.LastError, "cannot start build")
}

func TestCreateRunSuccessRecordsBuild(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	testCl := perftest.NewFakeClient(t)
	srv := newHTTPServer(t, appCl, testCl)
	in := validate.Input{
		Kubeconfig: fakeKubeconfig("https://api.example.com:6443"),
		SetupRuns: []run.SetupRun{
			{Name: "1user", Users: 1, Default: 1, Username: "setup"},
			{Name: "2k", Users: 2000, Default: 2000, Username: "cupcake"},
		},
	}

	// when
	id, status, msg := srv.createRun(context.TODO(), "alice", in)

	// then
	require.Equal(t, http.StatusCreated, status, msg)
	require.Equal(t, "tr-20260917-143000", id)
	got, _, err := run.Get(context.TODO(), appCl, appNS, id)
	require.NoError(t, err)
	require.Equal(t, run.PhasePrepare, got.Phase)
	require.Equal(t, "perf-job-1", got.BuildName)
	require.Equal(t, id, got.ImageTag)
	require.Equal(t, "alice", got.CreatedBy)
}

func TestListAndDetailFromConfigMaps(t *testing.T) {
	// given
	appCl := perftest.NewFakeClient(t)
	tr := &run.TestRun{
		ID:            "tr-1",
		CreatedAt:     time.Date(2026, 9, 17, 14, 30, 0, 0, time.UTC),
		CreatedBy:     "alice",
		APIServer:     "https://api.example.com:6443",
		Phase:         run.PhaseSucceeded,
		DeploySandbox: run.DeploySandbox{Job: "deploy-sandbox-tr-1", Status: run.StepSucceeded},
		SetupRuns: []run.SetupRun{
			{Name: "1user", Users: 1, Default: 1, Username: "setup", Job: "setup-0-tr-1", Status: run.StepSucceeded},
		},
	}
	require.NoError(t, run.Create(context.TODO(), appCl, appNS, tr, []byte("k")))
	require.NoError(t, run.PutCSV(context.TODO(), appCl, appNS, tr.ID, 0, "Item,Value\nUsers,1\n"))
	srv := newHTTPServer(t, appCl, perftest.NewFakeClient(t))

	t.Run("list", func(t *testing.T) {
		// given
		rec := httptest.NewRecorder()

		// when
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runs", nil))

		// then
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "tr-1")
		require.Contains(t, rec.Body.String(), "alice")
	})

	t.Run("detail", func(t *testing.T) {
		// given
		rec := httptest.NewRecorder()

		// when
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runs/tr-1", nil))

		// then
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "deploySandbox")
		require.Contains(t, rec.Body.String(), "setupRuns[0]")
		require.Contains(t, rec.Body.String(), "results-0.csv")
		require.Contains(t, rec.Body.String(), "Users,1")
	})

	t.Run("csv download", func(t *testing.T) {
		// given
		rec := httptest.NewRecorder()

		// when
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runs/tr-1/results/0", nil))

		// then
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
		require.Equal(t, "Item,Value\nUsers,1\n", rec.Body.String())
	})
}

func fakeKubeconfig(host string) []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: ` + host + `
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

func multipartBody(t *testing.T, fields, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	for name, content := range files {
		fw, err := w.CreateFormFile(name, name+".yaml")
		require.NoError(t, err)
		_, err = io.Copy(fw, strings.NewReader(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return body, w.FormDataContentType()
}

func TestEntrypointRejectsUnknownPhase(t *testing.T) {
	// given
	cmd := exec.Command("bash", "../build/perf-job/entrypoint.sh", "nope")

	// when
	out, err := cmd.CombinedOutput()

	// then
	require.Error(t, err)
	require.Contains(t, string(out), "unknown phase")
}
