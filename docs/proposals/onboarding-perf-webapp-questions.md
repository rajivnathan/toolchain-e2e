# Onboarding performance web app — sketch questions

**Status:** Resolved — all sketch questions decided
**Related:** [Sketch document](onboarding-perf-webapp-sketch.md)

## Q1: Where do the Test Run Jobs execute?

The goal says Jobs run on the test cluster. The alternative is Jobs on the host cluster that drive the test cluster through the uploaded kubeconfig. This choice sets the whole topology: image pull, metrics path, and what happens if the host app restarts.

### Option A: Jobs on the test cluster
- **Pro:** Load and API chatter stay on the cluster under test. Metrics can use Thanos inside the cluster plus a Job ServiceAccount. App disconnect or web-app restart does not kill the run. Matches the stated goal.
- **Con:** The host must remotely create namespace/SA/RBAC/Jobs. The test cluster must be able to pull the runner image. The host must retain credentials for the whole run in order to poll.

**Decision:** Option A — Jobs execute on the test cluster. The web app on the app cluster is only a controller. It persists Test Run records in ConfigMaps in its namespace so a web-app restart reloads in-progress and completed runs instead of losing them. App-cluster restart still does not cancel test-cluster Jobs.

_Considered and rejected: Option B (Jobs on the app cluster — hour-long remote API/metrics traffic, app Job failure aborts the test, contradicts “execute in the test cluster”)_

## Q2: How do phase Jobs run the existing setup pipeline?

The README is clone + `make dev-deploy-latest` + two `go run setup/main.go` invocations. The web app should not reimplement user provisioning or metrics. Cloning that repo in **every** phase Job repeats work Prepare already does when it builds the Job image.

### Option A: Fetch the repo once in Prepare; Jobs run `make` / prebuilt `setup` from that image
- **Pro:** Same commands as the laptop README, without paying git clone (and `go run` compile) on each Job. All phases share one SHA. DeploySandbox is just another Job in front of setup.
- **Con:** First Job waits on the image build (already required for tools/`oc`).

**Decision:** Option A — Prepare’s Git-source image build is the only fetch of `https://github.com/codeready-toolchain/toolchain-e2e`. Phase Jobs run `make dev-deploy-latest` or the prebuilt `setup` binary from that image. They do not clone. Provisioning and metrics stay in the existing `setup` tool. DeploySandbox is this app’s extra phase before setup.

_Considered and rejected: Option B (reimplement setup inside the web app — would drift from the CLI), Option C (clone again in each Job — extra minutes and a chance of different SHAs mid-run)_

## Q3: What do we call the cluster the web app deploys to, and does the type matter?

“Host” is already a Sandbox term (host-operator). The original question assumed we had to pick a dedicated tooling cluster vs a Sandbox host-operator cluster. The web app only needs a namespace of its own; the uploaded kubeconfig is used solely to start work on the **test** cluster.

### Option C: Any cluster; call it the app cluster; never run Test Runs there
- **Pro:** Avoids Sandbox “host” confusion. Where the app is deployed is an ops choice. The kubeconfig is only a pointer at the test cluster, so the app cluster’s role (Sandbox or not) does not change behavior.
- **Con:** The rule “never install Sandbox or run Test Run Jobs on the app cluster” must stay explicit, or someone could aim a Test Run at the same cluster that serves the UI.

**Decision:** Option C — drop the name “host cluster.” The web app runs in its own namespace on an **app cluster** (whatever OpenShift cluster it is deployed to). The kubeconfig is used only to kick off the Test Run on the target test cluster. The app never installs Sandbox operators and never creates Test Run Jobs on the cluster it is deployed into.

_Considered and rejected: Option A (require a dedicated tooling cluster — unnecessary once Test Runs cannot target the app cluster), Option B (require an existing Sandbox host-operator cluster — also unnecessary, and “host” is the wrong word)_

## Q4: One Job or a chain of Jobs per Test Run?

The Test Run includes deploy-Sandbox and one or two setup invocations. Kubernetes Jobs are one-shot; chaining them is orchestration the web app (or a wrapping Job) must own.

### Option B: One Job per step (DeploySandbox, then one Job per setup run), one Test Run
- **Pro:** Maps to the README when the list is the two presets. A later setup run starts only if the previous Job succeeded. Clearer UI timeline than one giant Job. Extra rows are more Jobs, not more phases.
- **Con:** Web app must create Jobs in order and pass artifacts (template ConfigMap, image, namespace) between them.

**Decision:** Option B — one Job per step, one Test Run. DeploySandbox always runs, then one Job per setup-run spec. The web app creates the next Job only after the previous Job succeeds. Automatic retry of a failed Job is out of scope for this sketch.

_Considered and rejected: Option A (one Job with sequential steps — failure at 2k looks like a single failed Job; harder to see or retry a phase), Option C (two Jobs bundling both setup invocations — weaker mapping to the spreadsheet’s 1-user vs 2k columns)_

## Q5: Does every Test Run include both the 1-user and the 2k-user setup?

The README captures metrics once after operators (1 user) and again at 2k users for the onboarding spreadsheet. That is the procedure this app is meant to automate — but a 2k run is expensive, and later we may want more than those two `setup` invocations.

### Option C: Ordered list of setup runs (defaults to the two README presets)
- **Pro:** 1-user and 2k are two configs, not two phases. N runs are extra rows. Debugging can drop the 2k row; a formal capture keeps both.
- **Con:** Slightly more UI than a three-way profile dropdown.

**Decision:** Option C — the form is an ordered list of setup runs. Default is the two README presets (1-user / `setup`, 2k / `cupcake`). The operator may add, remove, or edit rows. DeploySandbox always runs. One setup Job per row, in order, while phase is `SetupRunning`. A custom template is required only when any row has Custom users > 0 (see Q6).

_Considered and rejected: Option A (always both — too rigid for debugging and extra runs), Option B (profile dropdown only — cannot express N)_

## Q6: What else is on the form besides kubeconfig and template?

The goal lists two uploads. The setup CLI also needs `--workloads namespace:name` to include the onboarding operator’s CPU/memory, plus per-invocation flags (`--users`, `--custom`, `--username`, `--testname`). `--custom` defaults to 2000 in the CLI, but a run can skip the onboarding operator’s user-workload template (`--custom 0`).

### Option B: Setup-run rows plus a short optional set (`workloads`, `testname`)
- **Pro:** Each `setup` call is configurable. README presets fill two rows. Custom users default 0 per row.
- **Con:** Slightly more form; `workloads` must name a Deployment that exists after the operator is installed.

**Decision:** Option B — kubeconfig and at least one setup run are required. Each row has users, Custom users (default **0**), and username (unique in the list and valid for `setup` / Dev Sandbox username rules; README presets use `setup` then `cupcake`). The OpenShift Template YAML is **required only when any row has Custom users > 0**. Other optional fields: `workloads` (`namespace:name` pairs) and `testname`. `--default` defaults to the same as `--users`. `workloads` is passed to the setup Jobs; if set, setup already fails when that Deployment is missing. A README-matching 2k capture keeps both preset rows, sets the 2k row’s Custom users to **2000**, and uploads the template.

_Considered and rejected: Option A (files + profile only — CSV would omit the onboarding operator’s deployment metrics), Option C (most `setup` flags — the app would not be simpler than the CLI)_

## Q7: Is installing the onboarding operator in scope?

The README tells the operator to install their operator manually before running setup. The form has no field for operator manifests.

### Option A: Prereq only (document it; do not install)
- **Pro:** Matches today. The app stays generic. Avoids operator-specific install APIs (OLM, Helm, extra cluster-scoped CRs).
- **Con:** Easy to forget; a full 2k run with no operator under test is a wasted cluster.

**Decision:** Option A — installing the onboarding operator is a documented prereq, not a form field. The UI should state that clearly before submit. If `workloads` is set, the setup Job fails when that Deployment is missing (same check `setup` already does).

_Considered and rejected: Option B (upload operator manifests/bundle — large new scope, not in the stated goal)_

## Q8: Where does the Job image come from?

Jobs need `make dev-deploy-latest` (ksctl/oc, deploy manifests) and the setup binary. The web app has no local checkout to upload into the test cluster. Fetching git in every Job would repeat the Git-source build’s clone.

### Option B: Git-source BuildConfig on the test cluster; bake checkout + `setup`; Jobs do not clone
- **Pro:** One git fetch per Test Run (the build). Image has current `master` at Prepare time, plus a compiled `setup` so Jobs skip `go run`. No Quay pull secrets. No PVC.
- **Con:** Prepare waits on a cluster build (minutes). Test cluster must reach GitHub and pull UBI for the build.

**Decision:** Option B — during Prepare, a Git-source BuildConfig on the test cluster builds `build/perf-job/Dockerfile` from `https://github.com/codeready-toolchain/toolchain-e2e` (`master`). The image contains the checkout, tools, and a compiled `setup` binary. Phase Jobs pull that ImageStream tag and run `make` / `setup` from the baked path. They do not clone. Custom templates still come from the form ConfigMap, not from git.

_Considered and rejected: Option A (published image with baked checkout on Quay — lags `master`; every test cluster needs pull credentials), Option C (clone again in each Job — wasted time; SHAs can drift between phases), Option D (PVC shared checkout — extra storage; the image already has the tree)_

## Q9: Who can use the app, and how long does the kubeconfig live in the app namespace?

The upload is kube-admin of someone else’s test cluster. That is the main security property of the feature.

### Option A: GitHub oauth2-proxy; keep kubeconfig for the Test Run
- **Pro:** Not an anonymous internet form. Operators already have GitHub accounts; the app cluster need not have their OpenShift identities (fits Q3). `oauth2-proxy` as a sidecar in front of the app is a standard pattern (`quay.io/oauth2-proxy/oauth2-proxy`). Restricting `--github-org` / `--github-team` limits who can submit. Keeping the uploaded kubeconfig for the run is enough to create Jobs and poll; converting it to a test-cluster SA token is extra machinery.
- **Con:** During the run the app namespace holds kube-admin material for the test cluster. Any GitHub user in the allowed org/team who can reach the Route can submit. Without org/team restriction, any GitHub user could log in.

**Decision:** Option A — protect the UI with a GitHub [oauth2-proxy](https://oauth2-proxy.github.io/oauth2-proxy/configuration/providers/github) sidecar (`quay.io/oauth2-proxy/oauth2-proxy`), restricted to a GitHub org and/or team. Store the kubeconfig in the app namespace for the Test Run and use it to create Jobs and poll. Option C is not required: test clusters are ephemeral and torn down within a couple of hours, so long-lived kubeadmin credentials on a surviving cluster are not the threat. Test-cluster Jobs still use an in-cluster ServiceAccount for their own API calls.

_Considered and rejected: Option B (unauthenticated Route — anyone with the URL can upload a kubeconfig), Option C (convert kubeconfig to an SA token at submit — unnecessary given ephemeral test clusters), OpenShift OAuth (ties login to the app cluster’s user set; worse fit after Q3)_

## Q10: Where is Test Run state stored?

The UI must show progress and a summary after completion. The test cluster may be destroyed once the operator has copied numbers.

### Option A: App-namespace record (ConfigMap/Secret or equivalent) plus live poll of the test cluster
- **Pro:** History and CSV survive test-cluster teardown if copied back. The web app is the operator’s source of truth.
- **Con:** Must copy status/results into the app namespace during or at the end of the run.

**Decision:** Option A — Test Run ConfigMaps in the app namespace are the source of truth (progress, Job pointers, results CSV). The web app polls the test cluster while the cluster still exists and writes what it learns into those ConfigMaps. After the ephemeral test cluster is torn down, the UI still has the summary. Jobs do not write extra status ConfigMaps on the test cluster; the app polls Job conditions.

_Considered and rejected: Option B (test cluster only — summary vanishes when the cluster is deleted), Option C (dual-write — extra runner-side status objects this path does not need)_

## Q11: Can more than one Test Run be active?

A 2k-user run is load-generating. Two overlapping runs on the same test cluster would skew metrics. The web app may have many operators and many test clusters.

### Option A: One active Test Run per test cluster (API server URL); many across clusters
- **Pro:** Protects a given cluster from overlapping 2k-user runs. Different operators can run in parallel on different clusters.
- **Con:** The app polls several clusters. UI needs a list, not a single global status.

**Decision:** Option A — at most one active Test Run per test cluster (identified by API server URL). A second submit for that cluster is an error until the current run is terminal. Different test clusters may run at the same time. The UI is a list of Test Runs, not a single global status.

_Considered and rejected: Option B (one global run — unrelated operators block each other), Option C (unbounded overlap on the same cluster — skews results)_
