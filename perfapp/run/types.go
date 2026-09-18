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
	AppLabelValue = "onboarding-perfapp"
	LabelApp      = "app"
	LabelTestRun  = "testrun"
	LabelTestHost = "testhost"
	LabelPhase    = "phase"
	LabelSetupIdx = "setup-index"

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
	PhasePrepare       Phase = "Prepare"
	PhaseDeploySandbox Phase = "DeploySandbox"
	PhaseSetupRunning  Phase = "SetupRunning"
	PhaseSucceeded     Phase = "Succeeded"
	PhaseFailed        Phase = "Failed"
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

type DeploySandbox struct {
	Job    string     `json:"job"`
	Status StepStatus `json:"status"`
}

type SetupRun struct {
	Name     string     `json:"name,omitempty"`
	Users    int        `json:"users"`
	Default  int        `json:"default"`
	Custom   int        `json:"custom"`
	Username string     `json:"username"`
	Testname string     `json:"testname,omitempty"`
	Job      string     `json:"job,omitempty"`
	Status   StepStatus `json:"status,omitempty"`
}

type TestRun struct {
	ID            string        `json:"id"`
	CreatedAt     time.Time     `json:"createdAt"`
	CreatedBy     string        `json:"createdBy"`
	APIServer     string        `json:"apiServer"`
	Phase         Phase         `json:"phase"`
	SetupRunIndex int           `json:"setupRunIndex"`
	ImageTag      string        `json:"imageTag,omitempty"`
	BuildName     string        `json:"buildName,omitempty"`
	BuildPhase    string        `json:"buildPhase,omitempty"`
	BuildMessage  string        `json:"buildMessage,omitempty"`
	BuildReason   string        `json:"buildReason,omitempty"`
	BuildLog      string        `json:"buildLog,omitempty"`
	GitURI        string        `json:"gitURI,omitempty"`
	GitRef        string        `json:"gitRef,omitempty"`
	Workloads     []string      `json:"workloads,omitempty"`
	Testname      string        `json:"testname,omitempty"`
	TemplateFile  string        `json:"templateFile,omitempty"`
	DeploySandbox DeploySandbox `json:"deploySandbox"`
	SetupRuns     []SetupRun    `json:"setupRuns"`
	LastError     string        `json:"lastError,omitempty"`
}

func NewID(t time.Time) string {
	return "tr-" + t.UTC().Format("20060102-150405")
}

func ConfigMapName(id string) string { return "testrun-" + id }

func SecretName(id string) string { return "testrun-" + id + "-kubeconfig" }

func DeployJobName(id string) string { return "deploy-sandbox-" + id }

func SetupJobName(index int, id string) string {
	return fmt.Sprintf("setup-%d-%s", index, id)
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
	for _, sr := range tr.SetupRuns {
		if sr.Custom > 0 {
			return true
		}
	}
	return false
}
