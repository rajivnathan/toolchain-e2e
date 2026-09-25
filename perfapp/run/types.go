package run

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	AppLabelValue  = "onboarding-perfapp"
	LabelApp       = "app"
	LabelTestRun   = "testrun"
	LabelTestHost  = "testhost"
	LabelPhase     = "phase"
	LabelStepIndex = "step-index"

	JobAppValue = "setup-run"

	StatusKey = "status"
	KubeKey   = "kubeconfig"

	TestNamespace    = "sandbox-perf-test"
	RunnerSA         = "setup-runner"
	RunnerCRB        = "setup-runner-cluster-admin"
	BuildConfigName  = "perf-job"
	ImageStreamName  = "perf-job"
	TemplatesCMName  = "setup-run-templates"
	GitURI           = "https://github.com/codeready-toolchain/toolchain-e2e"
	GitRef           = "master"
	DockerfilePath   = "build/perf-job/Dockerfile"
	InternalRegistry = "image-registry.openshift-image-registry.svc:5000"
	HostOperatorNS   = "toolchain-host-operator"
	ResultsCSVKey    = "results.csv"
)

type Phase string

const (
	PhasePrepare   Phase = "Prepare"
	PhaseRunning   Phase = "Running"
	PhaseSucceeded Phase = "Succeeded"
	PhaseFailed    Phase = "Failed"
)

func (p Phase) Terminal() bool {
	return p == PhaseSucceeded || p == PhaseFailed
}

type StepStatus string

const (
	StepPending   StepStatus = ""
	StepRunning   StepStatus = "Running"
	StepSucceeded StepStatus = "Succeeded"
	StepFailed    StepStatus = "Failed"
)

type StepKind string

const (
	StepDeploySandbox StepKind = "deploy-sandbox"
	StepSetup         StepKind = "setup"
)

// Step is one Job in a Test Run. Kind selects how the Job is built.
// Users, Default, Custom, Username, and Testname apply to setup steps.
type Step struct {
	Kind     StepKind   `json:"kind"`
	Name     string     `json:"name,omitempty"`
	Users    int        `json:"users,omitempty"`
	Default  int        `json:"default,omitempty"`
	Custom   int        `json:"custom,omitempty"`
	Username string     `json:"username,omitempty"`
	Testname string     `json:"testname,omitempty"`
	Job      string     `json:"job,omitempty"`
	Status   StepStatus `json:"status,omitempty"`
}

type TestRun struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"createdAt"`
	CreatedBy    string    `json:"createdBy"`
	APIServer    string    `json:"apiServer"`
	Phase        Phase     `json:"phase"`
	StepIndex    int       `json:"stepIndex"`
	ImageTag     string    `json:"imageTag,omitempty"`
	BuildName    string    `json:"buildName,omitempty"`
	BuildPhase   string    `json:"buildPhase,omitempty"`
	BuildMessage string    `json:"buildMessage,omitempty"`
	BuildReason  string    `json:"buildReason,omitempty"`
	BuildLog     string    `json:"buildLog,omitempty"`
	GitURI       string    `json:"gitURI,omitempty"`
	GitRef       string    `json:"gitRef,omitempty"`
	Workloads    []string  `json:"workloads,omitempty"`
	Testname     string    `json:"testname,omitempty"`
	TemplateFile string    `json:"templateFile,omitempty"`
	Steps        []Step    `json:"steps,omitempty"`
	LastError    string    `json:"lastError,omitempty"`
}

func NewID(t time.Time) string {
	return "tr-" + t.UTC().Format("20060102-150405")
}

func ConfigMapName(id string) string { return "testrun-" + id }

func SecretName(id string) string { return "testrun-" + id + "-kubeconfig" }

func StepJobName(index int, id string) string {
	return fmt.Sprintf("step-%d-%s", index, id)
}

// Pipeline prepends the sandbox deploy step to the setup steps from the form.
func Pipeline(setups []Step) []Step {
	steps := make([]Step, 0, len(setups)+1)
	steps = append(steps, Step{Kind: StepDeploySandbox, Name: "deploy-sandbox"})
	for _, s := range setups {
		if s.Kind == "" {
			s.Kind = StepSetup
		}
		steps = append(steps, s)
	}
	return steps
}

func ResultsCMName(index int) string {
	return fmt.Sprintf("setup-run-results-%d", index)
}

func ResultsDataKey(index int) string {
	return fmt.Sprintf("results-%d.csv", index)
}

func JobImage(tag string) string {
	return fmt.Sprintf("%s/%s/%s:%s", InternalRegistry, TestNamespace, ImageStreamName, tag)
}

func NormalizeAPIServer(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/")
}

func HostLabel(apiServer string) string {
	sum := sha256.Sum256([]byte(NormalizeAPIServer(apiServer)))
	h := hex.EncodeToString(sum[:])
	if len(h) > 63 {
		return h[:63]
	}
	return h
}

func (tr *TestRun) SourceGitURI() string {
	if tr != nil && strings.TrimSpace(tr.GitURI) != "" {
		return strings.TrimSpace(tr.GitURI)
	}
	return GitURI
}

func (tr *TestRun) SourceGitRef() string {
	if tr != nil && strings.TrimSpace(tr.GitRef) != "" {
		return strings.TrimSpace(tr.GitRef)
	}
	return GitRef
}

func (tr *TestRun) NeedsTemplate() bool {
	for _, step := range tr.Steps {
		if step.Kind == StepSetup && step.Custom > 0 {
			return true
		}
	}
	return false
}
