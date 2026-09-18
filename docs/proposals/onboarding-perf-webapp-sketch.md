# Onboarding performance web app

**Status:** Sketch complete — ready for detailed design

Decisions: [onboarding-perf-webapp-questions.md](onboarding-perf-webapp-questions.md)

## What this is

A small web application, deployed in its own namespace on an **app cluster**, that runs the Dev Sandbox operator-onboarding performance procedure against a **test** cluster the user provides. “App cluster” means whatever OpenShift cluster serves the UI — not the Sandbox host-operator.

Today that procedure is a laptop workflow: provision a fresh OCP cluster, `oc login` as kubeadmin, clone this repo, `make dev-deploy-latest`, then `go run setup/main.go` (once with 1 user, once with 2000 users and a custom onboarding template). A typical 2k-user run takes about an hour. Results are a CSV meant for the Onboarding Performance Checklist spreadsheet.

This sketch replaces the laptop as the driver. After GitHub login, the operator opens a form, uploads a kubeconfig with kube-admin access to the test cluster, and an ordered list of **setup runs** (each is one call to the `setup` tool). The form defaults to the two README rows (1 user and 2000 users); the operator may add, remove, or edit rows. An OpenShift Template YAML (the onboarding operator’s user-workload template) is **required only when any setup run has Custom users greater than 0**. Optional form-level fields: `workloads`, `testname`.

On submit, the app validates those inputs, starts a **Test Run** (a chain of Jobs on the test cluster), tracks it to completion, and shows a results summary. Closing the browser does not stop the run. Test clusters are ephemeral (torn down within a couple of hours); the summary lives in ConfigMaps on the app cluster.

## Problem

The onboarding performance steps in [`setup/README.md`](../../setup/README.md) are long, easy to do incompletely, and tied to a human session (VPN, laptop sleep, local Go/Make/oc).

This web app is for a different user: someone who has a test cluster (and a template when any setup run uses Custom users > 0), and should not have to know the Makefile, the two-step `setup` invocation, or how to keep a process alive for an hour.

## How it relates to the existing system

Three layers stay distinct:

| Layer | Role |
| --- | --- |
| **Web app (this sketch)** | Form, validation, Test Run orchestration, progress UI, results summary. Lives in its own namespace on the app cluster. Stores Test Run records in ConfigMaps there so a restart can reload them and so the summary survives test-cluster teardown. |
| **Test Run Jobs** | Execute on the **test** cluster only. Deploy Sandbox operators and run the existing setup pipeline from a per-Test-Run image that already contains this repo. |
| **Existing `setup` pipeline** | Unchanged work: install onboarded operators, provision users, apply templates, sample Prometheus, write the CSV. |

Each phase is a Kubernetes Job on the test cluster (`restartPolicy: Never`, `backoffLimit: 0`) using a ServiceAccount with `cluster-admin`. The web app creates the next Job only after the previous one succeeds, and rejects a submit if that test cluster already has an active Test Run. **Prepare** fetches this repo **once** (a Git-source image build on the test cluster) and bakes the checkout plus a compiled `setup` binary into the Job image. Phase Jobs do not clone again; they run `make dev-deploy-latest` or `setup` from that image. All phases of a Test Run use the same git SHA. DeploySandbox (`make dev-deploy-latest` + ToolchainStatus Ready) runs before any setup Job.

```
 Browser (GitHub oauth2-proxy)
    │
    ▼
 App cluster                          Test cluster (user-provided, ephemeral)
 (web app namespace)                  (never the app cluster)
┌─────────────────────────┐          ┌──────────────────────────────────┐
│ Web app                 │ kubeconfig│ Phase Jobs (same namespace)     │
│  - form + validate      │─────────►│  Job: DeploySandbox (always)    │
│  - create Test Run      │  create  │  Job: Setup 0..N-1 (list)       │
│  - start next Job       │  next    │                                 │
│    only after success   │  Job     │  make / setup from baked image  │
│  - poll test cluster    │◄─────────│  Job conditions / pod logs      │
│  - write ConfigMaps     │  poll    │                                 │
│ ConfigMaps = source of  │          │                                 │
│  truth after teardown   │          │                                 │
└─────────────────────────┘          └──────────────────────────────────┘
```

Jobs run **on the test cluster**. The app cluster never executes clone/deploy/setup and never receives Sandbox operator install. A web-app restart must not cancel those Jobs; on startup the app reloads Test Run records from ConfigMaps in its namespace and resumes polling.

`make dev-deploy-latest` already installs published Sandbox operator images (`ksctl adm install-operator`) into `toolchain-host-operator` / `toolchain-member-operator`. The `setup` tool then verifies those operators, installs the onboarded-operator list, provisions users, and gathers metrics.

## Key concepts

### App cluster vs test cluster

- **App cluster** — whatever OpenShift cluster the web app is deployed to. The app runs in its own namespace (Route, Deployment, oauth2-proxy, Test Run ConfigMaps). Which cluster that is does not matter to the product: the app never installs Sandbox operators and never creates Test Run Jobs here.
- **Test cluster** — the cluster under test. The user provisions it (fresh OCP, as in the README) and uploads a kube-admin kubeconfig. It is **ephemeral** (torn down within a couple of hours). All destructive/load-generating work happens here, and only here. The kubeconfig is used solely to kick off and poll that Test Run.

### Test Run

A Test Run is one submission of the form. The web app writes it to **ConfigMaps in its namespace on the app cluster** (identity, setup-run list, phase, pointers to test-cluster Jobs, progress, results CSV). Those ConfigMaps are the **source of truth**: a restarted web app reloads them, and they still hold the summary after the test cluster is torn down. While the test cluster exists, the app **polls** Job conditions (and fetches results/logs as needed) and updates the ConfigMaps.

The load-generating work is a **chain of Jobs on the test cluster**: DeploySandbox, then one Job per setup run. A setup run is one `setup` invocation with its own `--users` / `--custom` / `--username` (and related flags). The form defaults to the two README configs (1-user and 2k); the operator can run N ≥ 1 by adding or removing rows. The web app creates the next Job only after the previous Job succeeds. Jobs share the test-cluster namespace, runner ServiceAccount, and (when any run has Custom users > 0) the template ConfigMap. If a Job fails, later Jobs are not created.

**Concurrency:** at most one **active** Test Run per test cluster (API server URL). A second submit for that cluster is an error until the current run is terminal. Different test clusters may run at the same time. The UI is a list of Test Runs.

Phases:

1. **Validate** — kubeconfig is kube-admin; at least one setup run; per run, Custom users is an integer with `0` ≤ custom ≤ users; usernames unique and valid for `setup`. If any custom > 0, the uploaded file is valid YAML / OpenShift Template. Reject if that test cluster already has an active Test Run.
2. **Prepare** — ensure a dedicated namespace, runner ServiceAccount, and `cluster-admin` binding on the test cluster; start a Git-source image build of this repo (checkout + compiled `setup`); store the template for later Jobs **when any custom > 0**.
3. **DeploySandbox** (Job) — always. From the baked checkout, run `make dev-deploy-latest`, wait until `oc get toolchainstatus -n toolchain-host-operator` shows `READY=True`.
4. **SetupRunning** — one Job per setup run, in list order (`setup-<index>`). From the baked checkout, run the prebuilt `setup` binary with that row’s flags (`--users`, `--default`, `--custom`, `--username`). `--template` only when that row’s custom > 0. Plus optional `--workloads` / `--testname`. A failed Job stops the chain.
5. **Succeeded / Failed** — terminal. The UI shows a summary built from the setup CSV(s) and Job outcomes.

Closing the browser must not cancel the Jobs; the operator can return and still see status and results.

### Form and validation

Required:

- **Kubeconfig** — must authenticate to the test cluster and act as kube-admin (not merely parse as YAML). A failed check does not start a Test Run.
- **Setup runs** — at least one row. Each row: **users** (≥ 1), **username** (unique in the list and valid for Dev Sandbox / `setup`), **Custom users** (default **0**, `0` ≤ custom ≤ users). `--default` defaults to the same as users. The form starts with two README preset rows (1-user / `setup`, 2k / `cupcake`); the operator may add, remove, or edit rows.
- **Onboarding template** — required **only when any setup run has Custom users > 0**. Must be valid YAML. Prefer also requiring an OpenShift `Template` (`setup/templates`) at submit time.

Optional (form-level, passed to every setup Job unless a row overrides `testname`):

- **`workloads`** — `namespace:name` pairs so the CSV includes the onboarding operator’s CPU/memory
- **`testname`** — suffix on result files

The **onboarding operator under test** must already be installed on the test cluster (same prereq as the README). The form does not accept operator manifests. The UI states this before submit. If `workloads` is set, the setup Job fails when that Deployment is missing.

### Runner image

Phase Jobs share one **per-Test-Run image** built on the **test** cluster during Prepare (Git-source BuildConfig of this repo, default `master`). That build is the only git fetch of this repo: the image contains the checkout, tools (`make`, `oc`, `ksctl`), and a compiled `setup` binary. Jobs do **not** clone this repo and do **not** `go run` (compile) again. DeploySandbox uses the baked `ksctl` (`USE_INSTALLED_KSCTL=true`). They all run the same SHA. The test cluster must reach GitHub and the Go module proxy for the build; Jobs themselves do not need GitHub for a toolchain-e2e clone. There is no per-run image build on the app cluster.

Custom template bytes come from the form when any setup run has Custom users > 0, not from git. They are mounted into the setup Jobs that pass `--template` (templates ConfigMap on the test cluster).

### Credentials

The UI is not on an open Route. A **GitHub oauth2-proxy sidecar** (`quay.io/oauth2-proxy/oauth2-proxy`) sits in front of the web app. Access is restricted to a GitHub org and/or team. The Service/Route must expose only the proxy so the app cannot be reached by skipping login.

The uploaded kubeconfig is kube-admin of the **test** cluster. The web app stores it in its namespace for the Test Run and uses it to create Jobs and poll. Converting it to a short-lived ServiceAccount token at submit is not required: the test cluster will be gone within a couple of hours. Jobs on the test cluster still use an in-cluster ServiceAccount (`cluster-admin`) for their own API calls, not the uploaded kubeconfig file.

### Status and results

App-namespace ConfigMaps are the source of truth. During the run, the web app polls the test cluster and updates those ConfigMaps; the UI reads them (phase, coarse progress, last error). Logs remain on the Job pods for debugging while the test cluster exists.

On completion, the app copies the setup CSV into the Test Run ConfigMap (or a sibling ConfigMap) so the summary survives teardown. The summary is the same kind of data `setup` already prints: user counts, timings, and Prometheus-derived CPU/memory figures, suitable for the onboarding spreadsheet. Prefer displaying the CSV table and offering a download over inventing a new report format.

## Rough behavior

1. Operator provisions a fresh test cluster and (separately) installs the onboarding operator under test, as today.
2. Operator signs in via GitHub (oauth2-proxy), opens the web app, uploads kubeconfig, edits the setup-run list (defaults to the two README rows), uploads a template if any Custom users > 0, optional fields, submits.
3. App rejects the submit if the kubeconfig is not kube-admin, the template is invalid when required, or that test cluster already has an active Test Run.
4. App records a Test Run (ConfigMap in its namespace) and uses the kubeconfig to **Prepare** the test cluster (including starting the image build and recording the Build name). The poller creates the **DeploySandbox** Job after that Build completes.
5. When that Job succeeds, the app creates setup Jobs in list order (`SetupRunning`). A failed Job stops the chain.
6. App polls the test cluster until the Test Run is terminal, writing progress and results into its ConfigMaps. The operator may leave and come back; after teardown the ConfigMaps still hold the summary.
7. UI shows success/failure, phase timeline, and the metrics summary / CSV download. Other operators’ runs on other test clusters appear in the same list.

If a phase fails, later phases do not start. Retrying a half-finished 2k-user provision with a new username prefix stays **manual** (today’s resume story); this sketch does not add automatic checkpoint/resume.

## What is out of scope

- Provisioning the test OCP cluster (still the operator’s AWS/`openshift-install` step)
- Changing user-provisioning concurrency, onboarded operator list, metric queries, or CSV columns
- Automatic resume of a partial user-provisioning run
- Replacing the onboarding spreadsheet / formal baseline comparison process
- Cleaning up users (`make clean-users`) or tearing down the test cluster from the UI
- Multi-cluster Sandbox host-operator/member-operator topology beyond what `dev-deploy-latest` already deploys (one member)
- Installing the onboarding operator under test (documented prereq; not a form field)
- Installing Sandbox operators or running Test Run Jobs on the app cluster
- Least-privilege RBAC for the runner (`cluster-admin` on the test-cluster Job SA is accepted)
- Converting the uploaded kubeconfig to a test-cluster ServiceAccount token at submit

## Success criteria

- An operator can start the documented onboarding performance procedure from a browser with a kube-admin kubeconfig, a list of setup runs (default two README presets), and a template only when any Custom users > 0
- Closing the browser after submit does not stop the Test Run; a web-app restart reloads ConfigMaps and resumes polling
- Sandbox is Ready (`ToolchainStatus`) before users are provisioned
- Results summary is comparable to today’s setup CSV (usable in the onboarding checklist) and remains after the test cluster is torn down
- Invalid kubeconfig never starts load-generating work. When any setup run has Custom users > 0, a missing or invalid template also never starts that work.
- A Test Run never installs Sandbox or creates Jobs on the app cluster
- Two overlapping Test Runs cannot target the same test cluster; different test clusters can run in parallel

## Decisions (index)

| # | Decision |
| --- | --- |
| Q1 | Jobs on the test cluster; app is a controller; Test Run ConfigMaps on the app cluster survive web-app restart |
| Q2 | Jobs run `make` / prebuilt `setup` from the Prepare image; they do not re-clone; DeploySandbox is a phase before setup |
| Q3 | Call it the **app cluster**; deploy anywhere; never run Test Runs or install Sandbox there |
| Q4 | One Job per step (DeploySandbox, then one Job per setup run), chained by the web app |
| Q5 | Ordered list of setup runs; form defaults to two README presets; operator can add/remove rows |
| Q6 | Per-run Custom users (default 0) and username (`TransformUsername` + unique); template required iff any custom > 0; optional `workloads`, `testname` |
| Q7 | Onboarding operator install is a documented prereq, not a form field |
| Q8 | One Git-source image build per Test Run on the test cluster; checkout + `ksctl` + `setup` binary baked in; Jobs do not clone |
| Q9 | GitHub oauth2-proxy **sidecar** (org/team); keep kubeconfig for the run; no SA-token conversion (clusters are ephemeral) |
| Q10 | App-namespace ConfigMaps are source of truth; poll the test cluster; Jobs do not write extra status objects |
| Q11 | One active Test Run per test cluster; many across clusters |
