# Onboarding performance web app — design questions

**Status:** Resolved — all design questions decided
**Related:** [Design document](onboarding-perf-webapp-design.md)

Sketch decisions live in [onboarding-perf-webapp-questions.md](onboarding-perf-webapp-questions.md) and are not reopened here.

## Q1: How does the setup CSV get from the Job into the app ConfigMap?

`setup` writes `tmp/results/*.csv` inside the Job pod and prints the table to stdout (after it restores stdout; during the run it redirects to files). The sketch requires that CSV to survive test-cluster teardown in an app-namespace ConfigMap. The poller needs a reliable fetch **before** the cluster disappears.

### Option C: Change `setup` to write a ConfigMap itself
- **Pro:** One code path for laptop and Job. The poller only copies bytes.
- **Con:** Extra flags (`--results-configmap`) before the web app Jobs can rely on them.

**Decision:** Option C — `setup` writes the results CSV into a ConfigMap on the cluster it is talking to. That CLI work is **phase 0 of this design** (a prereq for the web app Jobs). Poller copies that ConfigMap into the app-namespace Test Run record before the test cluster goes away. CSV columns stay unchanged.

_Considered and rejected: Option A (Job wrapper copies CSV — second writer, glob/path fragility), Option B (parse logs / exec — truncated, noisy, races with pod teardown)_

## Q2: How does the setup Job talk to Prometheus?

`setup` requires a bearer token (`--token` or `oc whoami -t`) and `GetPrometheusClient` uses the **external** `prometheus-k8s` Route. A Job has no `oc login`. `cluster-admin` on the Job SA is enough to *authorize* queries; the URL still has to work from inside the cluster.

### Option B: `--token` from the SA file; Thanos querier in-cluster URL
- **Pro:** `https://thanos-querier.openshift-monitoring.svc:9091` is reachable from the Job with the SA token. Survives without the public Route.
- **Con:** Requires a small setup change (env or flag to override the metrics URL) — still a patch in this repo.

**Decision:** Option B — Jobs pass `--token` from the SA token file and `--in-cluster-metrics`. Phase 0 adds that boolean: unset keeps today’s Route `prometheus-k8s` lookup; set GETs Service `thanos-querier` in `openshift-monitoring` and uses `https://<name>.<namespace>.svc:<port>`. TLS skip stays as today: `GetPrometheusClient` already uses `InsecureSkipVerify: true`. Do not load the service CA in v1. Do not pass a metrics URL on the command line.

_Considered and rejected: Option A (Prometheus Route from the Job — in-cluster Route/TLS/OAuth flakes), Option C (Prometheus port-forward sidecar — extra container, still needs a token)_

## Q3: What counts as kube-admin at submit?

The form asks for a kubeconfig “with kube-admin.” OpenShift’s `kube:admin` user is one way; any `cluster-admin` kubeconfig can install operators and run setup. Rejecting valid cluster-admin configs would strand operators who use a break-glass user that is not literally kubeadmin.

### Option A: SelfSubjectAccessReview equivalent to `cluster-admin`
- **Pro:** Matches what setup actually needs (create cluster-scoped objects, bind cluster-admin to the Job SA). Works for kubeadmin and other admin kubeconfigs.
- **Con:** Does not literally check the username `kube:admin`.

**Decision:** Option A — “kube-admin” means **cluster-admin equivalent**, checked with SelfSubjectAccessReview (enough to create a ClusterRoleBinding to `cluster-admin` / unrestricted SAR). Document that kubeadmin is the usual kubeconfig. Do not require the username `kube:admin`.

_Considered and rejected: Option B (require `kube:admin` — false negatives on other admin kubeconfigs), Option C (SAR plus `kube:` username — still rejects useful configs, little extra safety)_

## Q4: Username prefixes for setup Jobs?

`setup` names users `{prefix}-0001`, `{prefix}-0002`, … The README uses `--username setup` for the 1-user run and `--username cupcake` for 2k. The same prefix twice would collide on `{prefix}-0001`. Extra setup runs need their own prefixes.

### Option A: Per setup-run username; unique in the list; README presets pre-fill `setup` then `cupcake`
- **Pro:** Matches the documented procedure for the default two rows. Extra rows stay collision-free if usernames are unique.
- **Con:** Username is on the form (required per row). A leftover `{prefix}-0001` from a previous incomplete run on a *reused* cluster would still collide (README already says use a fresh cluster).

**Decision:** Option A — `--username` is a field on each setup-run spec. It must be unique in the list **and** pass the same `usersignup.TransformUsername` check `setup` uses at submit (do not wait for the Job to fatal). The two README preset rows pre-fill `setup` and `cupcake`. Test cluster is expected to be fresh.

_Considered and rejected: Option B (hard-code only those two prefixes — cannot express N), Option C (derive from Test Run ID — diverges from README `cupcake`)_

## Q5: Should a later setup Job skip operator install when an earlier one already ran?

An early README-style 1-user step installs onboarded operators and captures baseline metrics. A later `setup` invocation would try `EnsureOperatorsInstalled` again unless `--skip-install-operators` is set. Reinstall is usually idempotent but slows the start of a long run and can flake.

### Option B: Never skip; every setup Job installs operators
- **Pro:** Each setup Job is self-contained (a Test Run with a single 2k row still installs).
- **Con:** Duplicate work after the first successful setup Job; more OLM flake surface.

**Decision:** Option B — do not pass `--skip-install-operators`. Every setup Job runs the normal install path. `EnsureOperatorsInstalled` already no-ops when the operators are present, so a later Job does not reinstall from scratch after a successful earlier one.

_Considered and rejected: Option A (explicit skip after the first setup Job — unnecessary given idempotent install), Option C (always skip except the first — extra poller logic; breaks a list that starts with a row that still needs install)_

## Q6: How does the app orchestrate polling — HTTP ticker or controller-runtime?

The poller must chain Jobs and copy CSVs without a CRD. The app already talks to a **remote** cluster via kubeconfig, which controller-runtime’s usual single-cluster manager does not model well.

### Option A: `net/http` server + in-process ticker (single replica)
- **Pro:** Fits “no CRD,” one binary, easy to reason about. Restart resumes from ConfigMaps. Remote kubeconfig clients are just `client.New` per run.
- **Con:** Two replicas would double-create Jobs. Must set `replicas: 1`. Ticker is not a full reconcile queue.

**Decision:** Option A — one HTTP process with a ticker (and a pass on startup) that lists non-terminal Test Run ConfigMaps and polls remote Jobs. Deployment `replicas: 1` is required so two pollers cannot both create the next Job.

_Considered and rejected: Option B (TestRun CRD — sketch said no CRD; poor fit for a remote cluster), Option C (controller-runtime on ConfigMaps — still needs a remote ticker; little gain)_

## Q7: oauth2-proxy as a sidecar or a separate Deployment?

Sketch left topology open. The Service must not expose the app port.

### Option A: Sidecar in the same pod
- **Pro:** App can bind `127.0.0.1:8080`; only the proxy container is in the Service. One pod to deploy. Matches the discussion in sketch Q9.
- **Con:** Proxy and app share fate and resources. GitHub client secret is still a Secret volume.

**Decision:** Option A — oauth2-proxy sidecar in the web app pod. Route → Service port 4180 → proxy → `http://127.0.0.1:8080`. `--github-org` / `--github-team` and OAuth client credentials from a Secret. The app port is not on the Service.

_Considered and rejected: Option B (separate Deployment — easy to Route the app and skip GitHub login)_

## Q8: Where does the Job base image come from?

Jobs need `make`, `oc`, `ksctl`, this repo’s deploy tree, and the setup binary. `build/devsandbox-dashboard/Dockerfile` already installs a similar toolchain on UBI, but it is not this image. A Git-source BuildConfig already fetches the repo; cloning again in each Job is wasted time.

### Option D: Git-source BuildConfig on the test cluster; bake checkout + compiled `setup`
- **Pro:** No Quay pull secrets on ephemeral test clusters. Image lands in the internal registry (`image-registry.openshift-image-registry.svc:5000/...`). One git fetch per Test Run. Jobs skip clone and `go run` compile. All phases share the build’s SHA. Dockerfile still lives in this repo (`build/perf-job/Dockerfile`).
- **Con:** Prepare waits on a cluster build (minutes). Test cluster must reach GitHub and pull UBI. First Job cannot start until the ImageStream tag exists. Image is larger than tools-only.

**Decision:** Option D — during Prepare, the web app creates a Docker-strategy BuildConfig (git URI `https://github.com/codeready-toolchain/toolchain-e2e`, `dockerfilePath` `build/perf-job/Dockerfile`) and ImageStream in the test-cluster namespace, starts a build, records the Build name and ImageStream tag on the Test Run ConfigMap, and waits for Complete (poller). Jobs use `image-registry.openshift-image-registry.svc:5000/<ns>/perf-job:<tag>`. The image contains tools including **`ksctl` on `PATH`**, the checkout, and a compiled `setup` binary. Jobs `cd` into the baked path and run `make` / `setup`; they do **not** `git clone` this repo. DeploySandbox exports `USE_INSTALLED_KSCTL=true` so `make/ksctl.mk` does not clone or `go install` ksctl. One build per Test Run, shared by all phase Jobs. The **perfapp** image on the app cluster is still a normal published image (`build/perfapp/Dockerfile`).

_Considered and rejected: Option A (publish job image to Quay — every test cluster needs pull credentials; lags `master`), Option B (reuse dashboard/CI image — drift, wrong entrypoint), Option C (tools-only image + clone/`go run` per Job — repeats the build’s git fetch and compiles `setup` twice), Option E (PVC shared clone — extra object; the image already has the tree)_

## Q9: What namespace do Test Run Jobs use on the test cluster?

A fixed namespace is simple; a per-run namespace isolates leftover Jobs.

### Option A: Fixed `sandbox-perf-test`
- **Pro:** One place to look (`oc -n sandbox-perf-test get jobs`). SA/CRB created once. Simple overlap list.
- **Con:** Leftover Jobs from a previous attempt on a reused cluster (should be a fresh cluster).

**Decision:** Option A — all test-cluster objects (ns, SA, CRB, BuildConfig, ImageStream, Jobs, template/results ConfigMaps) live in **`sandbox-perf-test`**. Fresh test clusters make leftovers a non-issue.

_Considered and rejected: Option B (per-run namespace `perf-<id>` — extra CRBs, harder overlap/cleanup)_

## Q10: What happens to the kubeconfig Secret when the Test Run is terminal?

Sketch: keep kubeconfig for the run; clusters are ephemeral so SA-token conversion was skipped. After `Succeeded` / `Failed`, the poller no longer needs the test API except for optional log fetch.

### Option B: Keep the Secret until someone deletes the Test Run
- **Pro:** Can still poll logs if the cluster lives a bit longer.
- **Con:** kubeadmin bytes remain until manual cleanup.

**Decision:** Option B — keep the kubeconfig Secret for the life of the Test Run ConfigMap. Operators can still fetch logs while the ephemeral cluster exists. No TTL janitor in v1; deleting the run (manual later) should delete the Secret with it.

_Considered and rejected: Option A (delete Secret on terminal — cannot fetch logs after success), Option C (TTL on Secret and ConfigMap — extra janitor; CSVs may still be needed)_

## Q11: Job entrypoint — shell script or Go?

Each Job writes a kubeconfig for `make`/`ksctl` and runs `make` or the prebuilt `setup` binary from the baked checkout. `setup` itself writes the results ConfigMap (Q1).

### Option A: Shell entrypoint in the job image
- **Pro:** Natural fit for `make`, `oc`. Easy to read in the Dockerfile.
- **Con:** Weaker tests; quoting. `set -euo pipefail` required.

**Decision:** Option A — shell entrypoint in `build/perf-job/` (`set -euo pipefail`). Write in-cluster kubeconfig, `cd` to the baked checkout, `USE_INSTALLED_KSCTL=true`, run `make` or `setup`, wait for ToolchainStatus Ready with `oc`. Do not clone this repo. CSV is written by `setup` (`--results-configmap`), not by the script.

_Considered and rejected: Option B (Go helper — extra binary after Q1 moved CSV into setup), Option C (reimplement `make dev-deploy-latest` in Go — out of scope)_
