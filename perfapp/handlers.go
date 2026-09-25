package main

import (
	"context"
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/codeready-toolchain/toolchain-e2e/perfapp/remote"
	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"
	"github.com/codeready-toolchain/toolchain-e2e/perfapp/validate"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed templates/*.html
var templateFS embed.FS

type Server struct {
	AppClient       client.Client
	Namespace       string
	Templates       *template.Template
	NewTestClient   func([]byte) (client.Client, *rest.Config, error)
	NewInstantiator func(*rest.Config) (remote.Instantiator, error)
	Build           BuildConfiguration
	Now             func() time.Time
}

// BuildConfiguration is the Git source used for the test-cluster perf-job BuildConfig.
type BuildConfiguration struct {
	GitURI string
	GitRef string
}

func (b BuildConfiguration) URI() string {
	if strings.TrimSpace(b.GitURI) != "" {
		return strings.TrimSpace(b.GitURI)
	}
	return run.GitURI
}

func (b BuildConfiguration) Ref() string {
	if strings.TrimSpace(b.GitRef) != "" {
		return strings.TrimSpace(b.GitRef)
	}
	return run.GitRef
}

func (b BuildConfiguration) Validate() error {
	if b.URI() == "" || b.Ref() == "" {
		return fmt.Errorf("git URI and ref must not be empty")
	}
	return nil
}

func NewServer(appClient client.Client, namespace string, newTest ClientFactory) (*Server, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		AppClient:     appClient,
		Namespace:     namespace,
		Templates:     tmpl,
		NewTestClient: newTest,
		Now:           time.Now,
	}, nil
}

type ClientFactory func([]byte) (client.Client, *rest.Config, error)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.getForm)
	mux.HandleFunc("GET /runs", s.listRuns)
	mux.HandleFunc("GET /runs/{id}", s.getRun)
	mux.HandleFunc("GET /runs/{id}/results/{index}", s.downloadCSV)
	mux.HandleFunc("POST /runs", s.postRun)
	return mux
}

func (s *Server) Poller() *Poller {
	return &Poller{
		AppClient: s.AppClient,
		Namespace: s.Namespace,
		NewTestClient: func(kube []byte) (client.Client, error) {
			cl, _, err := s.NewTestClient(kube)
			return cl, err
		},
	}
}

func (s *Server) getForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "index.html", map[string]any{
		"Error":  r.URL.Query().Get("error"),
		"GitURI": s.Build.URI(),
		"GitRef": s.Build.Ref(),
	})
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := run.List(r.Context(), s.AppClient, s.Namespace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "runs.html", map[string]any{"Runs": runs})
}

type csvView struct {
	Index   int
	Key     string
	Preview string
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tr, cm, err := run.Get(r.Context(), s.AppClient, s.Namespace, id)
	if apierrors.IsNotFound(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var csvs []csvView
	for i, step := range tr.Steps {
		if step.Kind != run.StepSetup {
			continue
		}
		key := run.ResultsDataKey(i)
		if raw, ok := cm.Data[key]; ok {
			csvs = append(csvs, csvView{Index: i, Key: key, Preview: raw})
		}
	}
	s.render(w, "run.html", map[string]any{"Run": tr, "CSVs": csvs})
}

func (s *Server) downloadCSV(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		http.Error(w, "invalid results index", http.StatusBadRequest)
		return
	}
	_, cm, err := run.Get(r.Context(), s.AppClient, s.Namespace, id)
	if apierrors.IsNotFound(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	raw, ok := cm.Data[run.ResultsDataKey(index)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s"`, id, run.ResultsDataKey(index)))
	_, _ = w.Write([]byte(raw)) //nolint:gosec // Content-Type is text/csv, not HTML
}

func (s *Server) postRun(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	kube, _, err := readFile(r, "kubeconfig")
	if err != nil {
		redirectError(w, r, err.Error())
		return
	}
	templateBytes, templateName, err := readFile(r, "template")
	if err != nil && !errors.Is(err, http.ErrMissingFile) {
		redirectError(w, r, err.Error())
		return
	}
	steps, err := parseSetupSteps(r)
	if err != nil {
		redirectError(w, r, err.Error())
		return
	}
	workloads := splitCSV(r.FormValue("workloads"))
	in := validate.Input{
		Kubeconfig:   kube,
		Template:     templateBytes,
		TemplateName: templateName,
		Steps:        steps,
		Workloads:    workloads,
		Testname:     strings.TrimSpace(r.FormValue("testname")),
	}
	id, status, msg := s.createRun(r.Context(), userFromRequest(r), in)
	if status != http.StatusCreated {
		if status == http.StatusConflict {
			http.Error(w, msg, status)
			return
		}
		redirectError(w, r, msg)
		return
	}
	http.Redirect(w, r, "/runs/"+id, http.StatusSeeOther)
}

func (s *Server) createRun(ctx context.Context, createdBy string, in validate.Input) (string, int, string) {
	validated, err := validate.Submit(ctx, in, s.NewTestClient)
	if err != nil {
		return "", http.StatusBadRequest, err.Error()
	}
	overlap, err := run.FindOverlap(ctx, s.AppClient, s.Namespace, validated.APIServer)
	if err != nil {
		return "", http.StatusInternalServerError, err.Error()
	}
	if overlap != "" {
		return "", http.StatusConflict, fmt.Sprintf("an active Test Run %s already exists for this API server", overlap)
	}
	now := s.Now()
	id := run.NewID(now)
	tr := &run.TestRun{
		ID:           id,
		CreatedAt:    now.UTC(),
		CreatedBy:    createdBy,
		APIServer:    validated.APIServer,
		Phase:        run.PhasePrepare,
		ImageTag:     id,
		Workloads:    validated.Workloads,
		Testname:     validated.Testname,
		TemplateFile: validated.TemplateName,
		Steps:        run.Pipeline(validated.Steps),
		GitURI:       s.Build.URI(),
		GitRef:       s.Build.Ref(),
	}
	if err := run.Create(ctx, s.AppClient, s.Namespace, tr, in.Kubeconfig); err != nil {
		return "", http.StatusInternalServerError, err.Error()
	}
	inst, err := s.instantiator(validated.RESTConfig)
	if err != nil {
		_ = s.failCreate(ctx, tr, err.Error())
		return id, http.StatusInternalServerError, err.Error()
	}
	if err := remote.Prepare(ctx, validated.Client, inst, tr, validated.Template, validated.TemplateName); err != nil {
		_ = s.failCreate(ctx, tr, err.Error())
		return id, http.StatusInternalServerError, err.Error()
	}
	if err := run.UpdateStatus(ctx, s.AppClient, s.Namespace, tr); err != nil {
		return id, http.StatusInternalServerError, err.Error()
	}
	return id, http.StatusCreated, ""
}

func (s *Server) instantiator(cfg *rest.Config) (remote.Instantiator, error) {
	if s.NewInstantiator != nil {
		return s.NewInstantiator(cfg)
	}
	return remote.NewInstantiator(cfg)
}

func (s *Server) failCreate(ctx context.Context, tr *run.TestRun, msg string) error {
	tr.Phase = run.PhaseFailed
	tr.LastError = msg
	return run.UpdateStatus(ctx, s.AppClient, s.Namespace, tr)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.Templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func userFromRequest(r *http.Request) string {
	if u := r.Header.Get("X-Forwarded-User"); u != "" {
		return u
	}
	if u := r.Header.Get("X-Auth-Request-User"); u != "" {
		return u
	}
	return "unknown"
}

func readFile(r *http.Request, field string) ([]byte, string, error) {
	f, hdr, err := r.FormFile(field)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, "", err
	}
	name := "template.yaml"
	if hdr != nil && hdr.Filename != "" {
		name = filepath.Base(hdr.Filename)
	}
	return b, name, nil
}

func parseSetupSteps(r *http.Request) ([]run.Step, error) {
	users := r.Form["run.users"]
	if len(users) == 0 {
		return nil, errors.New("at least one setup run is required")
	}
	customs := r.Form["run.custom"]
	usernames := r.Form["run.username"]
	names := r.Form["run.name"]
	defaults := r.Form["run.default"]
	testnames := r.Form["run.testname"]
	out := make([]run.Step, len(users))
	for i := range users {
		u, err := strconv.Atoi(users[i])
		if err != nil {
			return nil, fmt.Errorf("setup run %d: invalid users", i)
		}
		sr := run.Step{Kind: run.StepSetup, Users: u, Username: getIndex(usernames, i), Name: getIndex(names, i), Testname: getIndex(testnames, i)}
		if c := getIndex(customs, i); c != "" {
			sr.Custom, err = strconv.Atoi(c)
			if err != nil {
				return nil, fmt.Errorf("setup run %d: invalid custom", i)
			}
		}
		if d := getIndex(defaults, i); d != "" {
			sr.Default, err = strconv.Atoi(d)
			if err != nil {
				return nil, fmt.Errorf("setup run %d: invalid default", i)
			}
		} else {
			sr.Default = sr.Users
		}
		out[i] = sr
	}
	return out, nil
}

func getIndex(vals []string, i int) string {
	if i >= 0 && i < len(vals) {
		return strings.TrimSpace(vals[i])
	}
	return ""
}

func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	r := csv.NewReader(strings.NewReader(raw))
	r.TrimLeadingSpace = true
	fields, err := r.Read()
	if err != nil {
		parts := strings.Split(raw, ",")
		var out []string
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	var out []string
	for _, f := range fields {
		if s := strings.TrimSpace(f); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func redirectError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?error="+url.QueryEscape(msg), http.StatusSeeOther)
}
