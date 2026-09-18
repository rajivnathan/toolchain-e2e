# Onboarding performance web app — design

**Status:** Final

**Sketch:** [onboarding-perf-webapp-sketch.md](onboarding-perf-webapp-sketch.md)
**Sketch decisions:** [onboarding-perf-webapp-questions.md](onboarding-perf-webapp-questions.md)
**Design decisions:** [onboarding-perf-webapp-design-questions.md](onboarding-perf-webapp-design-questions.md)

## Overview

A Go HTTP service, deployed in its own namespace on an **app cluster**, that drives the Dev Sandbox operator-onboarding performance procedure against a user-supplied **test** cluster.

The operator signs in with GitHub, submits a kube-admin kubeconfig, and an ordered list of **setup runs** (each is one call to the `setup` tool with its own flags). An OpenShift Template (the onboarding operator’s user-workload template) is **required only when any setup run has Custom users greater than 0**. The service validates inputs, creates a **Test Run** record, and chains Kubernetes Jobs **on the test cluster**: DeploySandbox, then one Job per setup run. Prepare builds a Job image that already contains this repository and a compiled `setup` binary; phase Jobs run `make dev-deploy-latest` / `setup` from that image (they do not clone). The service polls Job status, copies results into app-namespace objects, and shows a CSV summary after the ephemeral test cluster is gone.

Phase Jobs are Kubernetes Jobs (`restartPolicy: Never`, `backoffLimit: 0`, `cluster-admin` Job SA). The app fail-closes if that test cluster already has an active run. This repo is fetched **once** during Prepare (Git-source BuildConfig); Jobs invoke the prebuilt `setup` binary. **Setup CLI improvements that Jobs need** (results ConfigMap writer, in-cluster metrics lookup) **are phase 0 of this design.**

## Design Principles

1. **Jobs on the test cluster are the run.** Closing the browser or restarting the web app must not delete them.
2. **App-namespace objects are the source of truth.** Progress and CSVs live there so teardown of the test cluster does not lose the summary.
3. **Do not change the provisioning/metrics algorithm.** Same operator list, metric queries, and CSV *columns*. Each setup run chooses `--users` / `--default` / `--custom` / `--username`. README 1-user and 2k captures are two preset rows (Custom users default **0** on each; a matching 2k capture sets that row’s Custom users to 2000 and uploads the template). Setup CLI may gain flags/output sinks (ConfigMap writer, in-cluster Prometheus URL) as a prereq phase of this work.
4. **No Test Run work on the app cluster.** The app never installs Sandbox and never creates setup Jobs in its own cluster.
5. **Cluster objects stay simple.** No CRD. ConfigMaps + Secrets + labels. Single-replica Deployment.
6. **Fail closed on overlap.** At most one active Test Run per test-cluster API server URL.
7. **Reuse this repo’s conventions.** Go 1.26, UBI Dockerfiles under `build/`, deploy YAML under `deploy/`, fake client tests like `setup/test`.

## Architecture / How It Works

```
 Browser
    │  HTTPS Route
    ▼
 App namespace
┌──────────────────────────────────────────┐
│ Pod (replicas: 1)                        │
│  oauth2-proxy :4180  →  app :8080 (lo)   │
│  - POST /runs  validate + Prepare        │
│  - GET  /runs  list from ConfigMaps      │
│  - poller: Builds + Jobs → CM/CSV        │
│ ConfigMaps (status + csv)  Secrets (kube)|
└──────────────┬───────────────────────────┘
               │ kubeconfig (stored Secret)
               ▼
 Test cluster ns sandbox-perf-test
┌──────────────────────────────────────────┐
│ SA setup-runner + cluster-admin CRB      │
│ BuildConfig → ImageStream perf-job:<tag> │
│ CM templates + setup results             │
│ Job deploy-sandbox → setup-0 → setup-1… │
│ each: make / setup from baked image      │
└──────────────────────────────────────────┘
```

### Submit sequence

1. oauth2-proxy authenticates the browser (GitHub org/team). The app reads the user from `X-Forwarded-User` (or equivalent).
2. `POST` multipart form: kubeconfig file, **one or more setup runs** (users, custom, username, optional name / default / testname), template file (required iff any setup run has custom > 0), optional form-level `workloads` / `testname` (applied to every setup run unless a row overrides `testname`).
3. Parse kubeconfig (`clientcmd`). Fail if it does not load.
4. Build a client. Confirm **cluster-admin equivalent** access with SelfSubjectAccessReview (not a username check for `kube:admin`). Fail the submit if SAR denies. Document that kubeadmin is the usual kubeconfig.

5. Parse **setup runs** (at least one). For each run: `users` ≥ 1; `custom` empty → `0`; `0` ≤ `custom` ≤ `users`; `default` empty → same as `users`; `username` required, unique in the list, and accepted by the same check `setup` uses (`usersignup.TransformUsername` must return the prefix unchanged — lowercase alphanumeric or `-`, not starting with `openshift`/`kube`/`default`/`redhat`/`sandbox`, not ending with `admin`, not numeric-only, max 20 characters). If any `custom` > 0, parse the uploaded template: valid YAML and OpenShift `Template` (`setup/templates.GetTemplateFromContent`). If every `custom` is 0, do not require or store a template (an unused upload is ignored). The HTML form defaults to two README preset rows (1-user and 2k); the operator may add, remove, or edit rows.
6. Normalize the API server URL. List app-namespace Test Run ConfigMaps for that URL that are not terminal. If any exist, return 409.
7. Allocate a Test Run ID (`tr-<timestamp>`). Write:
   - Secret `testrun-<id>-kubeconfig` (kubeconfig bytes)
   - ConfigMap `testrun-<id>` (status JSON: setupRuns, GitHub user, API URL, phase `Prepare`)
8. Using the kubeconfig:
   - Ensure namespace, ServiceAccount `setup-runner`, ClusterRoleBinding `cluster-admin`.
   - List Jobs labeled `app=setup-run` in that namespace; if any are active, fail (belt-and-braces with step 6) **before** starting a build.
   - Ensure BuildConfig `perf-job` (git source: this repo, `dockerfilePath: build/perf-job/Dockerfile`) and ImageStream `perf-job`. Start a build. **Write the unique ImageStream tag and Build name onto the Test Run ConfigMap before returning.** If start-build (or Ensure ns/SA/CRB/BuildConfig) fails, set phase `Failed` and lastError on that ConfigMap in the **same request** and return an error (not 201).
   - If any setup run has custom > 0, write ConfigMap `setup-run-templates` (key = basename) for Jobs that pass `--template` to mount at `/templates`.
9. Return 201 with the run ID. Do **not** wait for the image build to Complete or for Jobs. The poller creates **DeploySandbox** once the Build succeeds.

### Poller sequence

A ticker in the same process (and on startup) lists non-terminal Test Run ConfigMaps.

**Job create is idempotent.** Job names are deterministic from the Test Run ID (`deploy-sandbox-<id>`, `setup-<index>-<id>`). Before creating a Job, GET it by name in `sandbox-perf-test` (labels `testrun=<id>`, `phase=DeploySandbox|SetupRunning`, and for setup Jobs `setup-index=<n>`). If it already exists, do not create another: record the name on the ConfigMap if missing, set the phase if needed, and poll that Job. A restart after create-but-before-ConfigMap-write must not start a second Job.

1. Load the kubeconfig Secret. If the test API is gone, mark the run `Failed` with lastError (cluster torn down mid-run) unless results were already copied.
2. If phase is `Prepare` (image build): GET the Build. If running, continue. If Failed, set phase `Failed`. If Complete, **GET DeploySandbox** (`deploy-sandbox-<id>`). Create it only if it is missing (image = internal ImageStream tag from this Test Run), then set phase `DeploySandbox`.
3. If phase is `DeploySandbox` or `SetupRunning`: GET the Job for the current step (`deploy-sandbox-<id>` or `setup-<setupRunIndex>-<id>`). If still active, optionally copy a short log tail into status and continue.
4. If that Job **Failed**, set phase `Failed`, lastError from Job message / logs. Do not create later setup Jobs.

5. If that Job **Succeeded**:
   - If it was a setup Job, GET the results ConfigMap for that index and copy the CSV into the app ConfigMap (key `results-<index>.csv`).
   - After DeploySandbox: set `setupRunIndex` to `0`, phase `SetupRunning`, and **GET `setup-0-<id>`** — create it only if missing.
   - After a setup Job: increment `setupRunIndex`. If it equals `len(setupRuns)`, set phase `Succeeded`. Else **GET `setup-<index>-<id>`** and create it only if missing.

The kubeconfig Secret is **kept** for as long as the Test Run ConfigMap exists so logs can still be fetched while the test cluster is up. No automatic delete on terminal.

Web-app restart: Deployment comes back, poller lists ConfigMaps, resumes. It never deletes test-cluster Jobs. It GETs existing Jobs before creating any.

### Job sequence (test cluster)

Shared: `restartPolicy: Never`, `backoffLimit: 0`, no `ttlSecondsAfterFinished`, no `activeDeadlineSeconds`. SA `setup-runner`. Image: `image-registry.openshift-image-registry.svc:5000/<ns>/perf-job:<tag>` from this Test Run’s Build. Name `deploy-sandbox-<id>` or `setup-<index>-<id>`. Label `app=setup-run`, `testrun=<id>`, `phase=<DeploySandbox|SetupRunning>`; setup Jobs also `setup-index=<n>`. Compile happens in the image build, not in setup Jobs. **Resources:** DeploySandbox on the order of **1Gi** memory; a setup Job with `--users` 2000 on the order of **2–4Gi** memory and **1–2 CPU** (user-provisioning load); smaller `--users` can match DeploySandbox.

Entrypoint is a **shell script** in the job image (`set -euo pipefail`): write an in-cluster kubeconfig from the SA token/CA (for `oc` / `make` / `ksctl`), `cd` into the baked checkout (for example `/opt/toolchain-e2e`) so `setup` finds `setup/resources/user-workloads.yaml`. Do **not** `git clone`. Export `USE_INSTALLED_KSCTL=true` so `make ksctl` uses the `ksctl` binary in `PATH` instead of `git clone` / `go install` (`make/ksctl.mk`).

- **DeploySandbox:** `make dev-deploy-latest` then wait until ToolchainStatus `toolchain-status` in `toolchain-host-operator` has `Ready=True` (`oc` poll).
- **Setup (index i):** the prebuilt `setup` binary with flags from `setupRuns[i]`: `--users`, `--default`, `--custom`, `--username`, `--interactive=false`, `--token` from the SA token file, `--results-configmap=setup-run-results-<i>`, `--in-cluster-metrics`. `--template` is `/templates/<basename>` only when that run’s `--custom` is greater than 0. Form-level `--workloads` / `--testname` unless the row overrides `testname`.

`--username` is per setup run: unique in the list and valid under `TransformUsername` (`setup` names users `{prefix}-0001`). README preset rows use `setup` then `cupcake`. Every setup Job runs operator install (`EnsureOperatorsInstalled` re-applies and waits; already-Succeeded CSVs finish quickly). Do not pass `--skip-install-operators`.

## Core Concepts

### App process

One Deployment, **replicas: 1** (two pollers could still race even with GET-before-create). The process is `net/http` plus an in-process ticker (and a pass on startup). **oauth2-proxy is a sidecar** in the same pod (`quay.io/oauth2-proxy/oauth2-proxy`). Route → Service port 4180 → proxy → app on `127.0.0.1:8080`. The app port is not on the Service. GitHub org/team and OAuth client credentials come from a Secret.

The app’s ServiceAccount on the **app cluster** only needs to manage ConfigMaps/Secrets in its namespace. All test-cluster API calls use the uploaded kubeconfig, not app-cluster `cluster-admin`.

### Test Run record

| Object | Namespace | Role |
| --- | --- | --- |
| ConfigMap `testrun-<id>` | app | Status JSON + optional CSV data keys |
| Secret `testrun-<id>-kubeconfig` | app | Uploaded kubeconfig; kept until the Test Run ConfigMap is deleted |
| BuildConfig `perf-job` + ImageStream `perf-job` | test `sandbox-perf-test` | Git-source build of `build/perf-job/Dockerfile`; bakes this repo + `setup` binary; Jobs pull the resulting tag |
| ConfigMap `setup-run-templates` | test `sandbox-perf-test` | Custom template bytes when any setup run has custom > 0, mounted at `/templates` on Jobs that pass `--template` |
| ConfigMap `setup-run-results-<index>` | test `sandbox-perf-test` | CSV bytes written by that setup Job (`--results-configmap`) |
| Secret (none) on test cluster for kubeconfig | — | Jobs use in-cluster SA |

Status JSON (best guess). One object per step — **no top-level `jobs` map**. Job names are deterministic (`deploy-sandbox-<id>`, `setup-<index>-<id>`), so the poller can GET before `job` is written; the field is still stored so the UI can link logs without recomputing. `setupRunIndex` is the poller cursor (which setup row is in flight).

```json
{
  "id": "tr-20260917-143000",
  "createdAt": "2026-09-17T14:30:00Z",
  "createdBy": "alice",
  "apiServer": "https://api.sandbox-test.devcluster.openshift.com:6443",
  "phase": "SetupRunning",
  "setupRunIndex": 1,
  "imageTag": "20260917-143000",
  "buildName": "perf-job-1",
  "workloads": ["rhoai-operator:rhoai"],
  "testname": "",
  "deploySandbox": {
    "job": "deploy-sandbox-tr-20260917-143000",
    "status": "Succeeded"
  },
  "setupRuns": [
    {
      "name": "1user",
      "users": 1,
      "default": 1,
      "custom": 0,
      "username": "setup",
      "job": "setup-0-tr-20260917-143000",
      "status": "Succeeded"
    },
    {
      "name": "2k",
      "users": 2000,
      "default": 2000,
      "custom": 0,
      "username": "cupcake",
      "job": "setup-1-tr-20260917-143000",
      "status": "Running"
    }
  ],
  "lastError": ""
}
```

Phases: `Prepare` | `DeploySandbox` | `SetupRunning` | `Succeeded` | `Failed`.

While `SetupRunning`, `setupRunIndex` is the setup Job in flight (or just finished). The UI timeline is `deploySandbox` then each `setupRuns[i]` — spec, Job name, and status stay on the same object. 1-user and 2k are two preset **setup run** specs, not phases.

Labels on the ConfigMap: `app=onboarding-perfapp`, `testrun=<id>`, `testhost=<sha256-of-api-host>` (for overlap queries).

### UI

Server-rendered HTML (Go `html/template`), no separate SPA:

- `GET /` — form + note that the onboarding operator must already be installed; default **two setup-run rows** (README 1-user and 2k, Custom users `0`); operator can add/remove rows; template file required only when any row has Custom users > 0
- `GET /runs` — list (id, GitHub user, API host, phase, createdAt)
- `GET /runs/<id>` — phase timeline (each setup run), lastError, CSV table(s), download links
- `POST /runs` — create

oauth2-proxy handles `/oauth2/`. The app does not implement GitHub OAuth itself.

### Job image

`build/perf-job/Dockerfile` in this repo is a UBI image with the tools `make` needs (`make`, `oc`, **`ksctl` on `PATH`**, Go only as required by those scripts) **plus this repository’s checkout and a compiled `setup` binary**. It is **not** published to Quay for Jobs. During Prepare, the web app creates a Git-source Docker-strategy BuildConfig on the **test cluster** (URI `https://github.com/codeready-toolchain/toolchain-e2e`, `dockerfilePath: build/perf-job/Dockerfile`), starts a build to ImageStream `perf-job:<tag>` unique to the Test Run, and the poller waits for Complete before the first Job.

That build is the only git fetch of this repo for the Test Run. OpenShift already copies the Git-source tree into the build; the Dockerfile `COPY`s it to a fixed path (for example `/opt/toolchain-e2e`), installs `ksctl`, and `go build`s `./setup`. The test-cluster build must reach GitHub, pull UBI, and reach the Go module proxy (`GOPROXY`) to compile `setup`. Phase Jobs pull the tag and run `make` / `setup` from that path. They do **not** clone this repo and do **not** compile `setup` again. DeploySandbox sets `USE_INSTALLED_KSCTL=true` so `make` does not fetch `ksctl` again. All phases share the same SHA (the build’s `master` at Prepare time). Do not use a PVC to share a checkout: the image already has it, and Jobs are sequential anyway.

Jobs pull from the in-cluster registry. Jobs do not need GitHub for a toolchain-e2e clone (they still need network for operator/image pulls during DeploySandbox and setup). Do not size setup Jobs for `go run` memory; size them for provisioning (see Job sequence).

The perfapp image (`build/perfapp/Dockerfile`) is a normal published image on the **app cluster** only.

Test-cluster Jobs, BuildConfig, ImageStream, SA, and ConfigMaps all live in the fixed namespace **`sandbox-perf-test`**.

### Metrics

The `setup/metrics` gatherer still runs inside the setup Jobs (5-minute samples, optional 15-minute settle, same queries plus `--workloads`). The web app does not query Prometheus. Jobs pass `--in-cluster-metrics` so `setup` looks up Service `thanos-querier` in `openshift-monitoring` (`https://<name>.<namespace>.svc:<port>`) instead of Route `prometheus-k8s`. The client still uses the SA token. `setup` writes the CSV to a test-cluster ConfigMap; the poller copies it into the app-namespace Test Run ConfigMap.

### Setup runs

A **setup run** is one invocation of the `setup` binary: `--users`, `--default`, `--custom`, `--username`, optional `--template` / `--workloads` / `--testname`. The Test Run stores an ordered list. The poller is a loop over that list while phase is `SetupRunning` — it does not hard-code 1-user vs 2k.

The HTML form defaults to the two README presets (below). Extra rows are more setup runs (N ≥ 1). Username must be unique across the list and pass `TransformUsername`.

### Validation details

- Kubeconfig: `clientcmd` load + client to the API. Honor the kubeconfig’s CA/insecure-skip as written (do not force skip). Then SelfSubjectAccessReview for cluster-admin equivalent. Do not require username `kube:admin`.
- Setup runs: at least one. Per run: `users` ≥ 1; `0` ≤ `custom` ≤ `users`; `username` required, unique in the list, and unchanged by `usersignup.TransformUsername` (same rules as `setup/cmd/root.go`).
- Template: required **iff any setup run has custom > 0**. Then `GetTemplateFromContent` — Kind must be `Template`. If every custom is 0, do not require a file.
- `workloads`: each value `namespace:name` (same split as `setup/cmd/root.go`). Existence of the Deployment is checked by setup after operators exist, not at submit (the onboarding operator may be installed but an early setup Job also installs other operators).

### Defaults (per setup run)

Shared on every setup Job: `--interactive=false`, `--skip-install-operators` omitted (false), `--in-cluster-metrics`, `--results-configmap=setup-run-results-<index>`, `--workloads` / `--testname` from the form unless the row overrides `testname`. `--template` only if that run’s `custom` > 0.

README preset rows (form defaults):

| Field | Preset `1user` | Preset `2k` |
| --- | --- | --- |
| `--users` / `--default` | 1 | 2000 |
| `--custom` | 0 (editable) | 0 (editable; set 2000 for a README-matching capture) |
| `--username` | `setup` | `cupcake` |

A README-matching 2k capture leaves the 1-user row as-is, sets the 2k row’s Custom users to **2000**, and uploads the template.

## Implementation Plan

### Package layout

| Path | Responsibility |
| --- | --- |
| `setup/results` (+ `setup/cmd`) | **Phase 0:** extend the existing results package so the same CSV bytes also go to a ConfigMap when a name is set; still write local file + stdout for laptop use |
| `perfapp/` | HTTP server, poller, HTML templates, kubeconfig/template validation |
| `perfapp/run/` | Test Run ConfigMap/Secret CRUD, overlap check, phase machine |
| `perfapp/remote/` | Ensure test ns/SA/CRB, create/list Jobs, fetch logs, copy results ConfigMaps |
| `build/perfapp/Dockerfile` | UBI multi-stage: compile `perfapp`, runtime image |
| `build/perf-job/Dockerfile` | UBI image: tools + baked checkout + compiled `setup`; shell entrypoint; built on the test cluster |
| `deploy/onboarding-perfapp/` | Namespace, Deployment (app + proxy), Service, Route, RBAC, oauth2 Secret placeholders |
| `setup/*_test.go` / `perfapp/*_test.go` | Fake client: ConfigMap CSV writer; overlap; phase transitions; reject bad kubeconfig/template |

Do not put the HTTP app under `setup/cmd`; that package stays the performance CLI.

`make` targets: existing setup tests, plus `perfapp-build`, `perfapp-image`, `perf-job-image` (names can match repo style).

### Setup CLI flags the Jobs must pass

Always `--interactive=false`. Always `--token` from the SA token file so metrics do not call `oc whoami -t`. Jobs pass `--in-cluster-metrics` (laptop omits it and keeps the Prometheus Route lookup). **Phase 0:** `--results-configmap` (name) in the Job namespace so `OutputResults` also writes ConfigMap data key `results.csv` (local `tmp/results/` remains for laptop runs); `--in-cluster-metrics` switches `getPrometheusEndpoint` from Route `prometheus-k8s` to Service `thanos-querier` in `openshift-monitoring`. No TTY; `uiprogress` still runs but logs are the Job pod.

### Tests

- Validation: malformed kubeconfig, SAR denial, empty setupRuns, duplicate usernames, username rejected by `TransformUsername`, custom > users, missing template when any custom > 0, non-Template YAML when a template is required; all-custom-0 succeeds without a template.
- Overlap: second create with same API host while phase is `SetupRunning` → error; different API host → allowed.
- Phase machine: DeploySandbox success → create `setup-0-<id>` only if missing; that Job’s failure → no `setup-1`; after last setup Job succeeds → `Succeeded`.
- Poller: Job Succeeded copies CSV keys; missing kubeconfig Secret after teardown + no CSV → Failed; Prepare Complete with DeploySandbox already present → do not create a second Job.
- HTTP: unauthenticated requests never hit the app if the Service only exposes the proxy (document; e2e optional).

No live 2k-user test in CI (same as today’s setup tool).

### Rollout order

0. **Setup CLI (prereq):** ConfigMap results writer (`--results-configmap`); `--in-cluster-metrics` (default Route on laptop, Service `thanos-querier` in Jobs); unit tests with fake client. Keep laptop file/stdout behavior.
1. `build/perf-job/Dockerfile` + entrypoint; prove a Git-source BuildConfig + DeploySandbox Job on a throwaway cluster.
2. `perfapp` ConfigMap/Secret types, overlap, Prepare (including start-build + wait).
3. Poller + Job create for DeploySandbox and N setup runs; copy each setup results ConfigMap into the Test Run record.
4. HTML list/detail + CSV download.
5. `deploy/onboarding-perfapp` + oauth2-proxy (perfapp image published for the app cluster).
6. README in `perfapp/` (not a rewrite of `setup/README.md`).

## Decisions (index)

| # | Decision |
| --- | --- |
| Q1 | `setup` writes results ConfigMap (`--results-configmap`); poller copies to the app; phase 0 of this design |
| Q2 | Job uses SA token + `--in-cluster-metrics` (lookup Service `thanos-querier`); laptop keeps the Prometheus Route default |
| Q3 | Kube-admin = cluster-admin equivalent via SelfSubjectAccessReview |
| Q4 | `--username` per setup run; unique in the list; `TransformUsername` at submit; README presets `setup` then `cupcake` |
| Q5 | Do not pass `--skip-install-operators`; `EnsureOperatorsInstalled` is idempotent |
| Q6 | `net/http` + ticker; `replicas: 1` |
| Q7 | oauth2-proxy sidecar; app on localhost only |
| Q8 | Git-source BuildConfig on the test cluster; bake checkout + `ksctl` + `setup`; `USE_INSTALLED_KSCTL=true`; Jobs do not clone |
| Q9 | Namespace `sandbox-perf-test` |
| Q10 | Keep kubeconfig Secret until the Test Run ConfigMap is deleted |
| Q11 | Shell entrypoint in the job image |
| (sketch Q5–Q6) | Ordered list of setup runs (default two README presets); template required iff any custom > 0 |

Sketch decisions remain in [onboarding-perf-webapp-questions.md](onboarding-perf-webapp-questions.md).
