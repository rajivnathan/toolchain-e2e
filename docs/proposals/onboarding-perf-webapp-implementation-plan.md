# Onboarding performance web app — implementation plan

**Status:** Plan complete — ready for `/implement-with-tasks`

**Related:** [Design](onboarding-perf-webapp-design.md) · [Implementation questions](onboarding-perf-webapp-implementation-questions.md)

## Goal

Land the onboarding-perf HTTP app (`perfapp/`) and the setup CLI flags its Jobs need: ConfigMap CSV output, `--in-cluster-metrics`, a test-cluster Git-source job image, and a single-replica controller that validates a kubeconfig, chains DeploySandbox plus N setup Jobs, and copies CSVs into app-namespace ConfigMaps.

## Preconditions

- Design status is **Final** ([onboarding-perf-webapp-design.md](onboarding-perf-webapp-design.md)). Package paths use `perfapp/` (not `webapp/`).
- Go 1.26 as in `go.mod`.
- Phase 0 must not change CSV columns, operator templates, or provisioning concurrency.
- Laptop `go run setup/main.go` must keep writing `tmp/results/*.csv` and stdout when the new flags are omitted.
- No live 2k-user test in CI.
- **PR 1 is phase 0 only.** Do not merge `perfapp/` until `--results-configmap` and `--in-cluster-metrics` are on `master` (the Git-source BuildConfig clones that ref).

## Current-state map

| Today | Change |
| --- | --- |
| `setup/results/results.go` — file + stdout only (`OutputResults`, `cfg.ResultsFilepath()`) | Also write ConfigMap data key `results.csv` when `--results-configmap` is set |
| `setup/cmd/root.go` — flags; `setup()` calls `results.New` / `OutputResults` | Add `--results-configmap` and `--in-cluster-metrics`; pass in-cluster client + namespace (Job namespace) |
| `setup/metrics/client.go` — `getPrometheusEndpoint` always GETs Route `prometheus-k8s` | If `--in-cluster-metrics`, GET Service `thanos-querier` and build `https://<name>.<ns>.svc:<port>`; else keep Route (laptop) |
| `setup/test/fakeclient.go` — scheme has toolchain, quota, OLM; no corev1 | Add corev1 (and later OpenShift `build/v1` for perfapp fakes) |
| `setup/main.go` + `make test-setup` (`make/test.mk`) | Phase 0 tests ride on existing `go test ./setup/...` |
| No `perfapp/`, no `build/perf-job/`, no `build/perfapp/`, no `deploy/onboarding-perfapp/` | Add as designed |
| `build/devsandbox-dashboard/Dockerfile` | **Do not reuse** for Jobs; copy only the idea (UBI + `oc` + Go) |
| `deploy/devsandbox-dashboard/` | Pattern for Route/Service/Deployment; new tree for this app + oauth2-proxy sidecar |
| `make/ksctl.mk` — `USE_INSTALLED_KSCTL` defaults false | Image installs `ksctl` `@master` at **image build** time; Job entrypoint sets `USE_INSTALLED_KSCTL=true` |

## Sequencing strategy

**PR-split first, then layer cake.** Phase 0 is a standalone `setup/` PR so Git-source Jobs can clone `master` with the new flags. After that, design rollout 1→6: Jobs cannot copy CSVs until the CLI exists; the poller cannot create Jobs until the image and Prepare APIs exist; HTML can sit on ConfigMaps once the poller writes them. Merge the job image on a local `podman build`; prove BuildConfig on a throwaway cluster as a README checklist, not a PR gate. Do not start a live 2k run in CI.

## Phases

### Phase 0 — Setup CLI sinks (Jobs’ prereq) — **own PR**

**Goal:** `setup` can write the same CSV to a ConfigMap and use `--in-cluster-metrics` without changing laptop defaults.

**Changes:**

- `setup/cmd/root.go` — `--results-configmap` (name; namespace = in-cluster / kubeconfig current namespace, i.e. Job ns `sandbox-perf-test`); `--in-cluster-metrics` (bool; default false = Route lookup).
- `setup/results/results.go` — after file/stdout write, if the name is set, create/update ConfigMap data key `results.csv` using the existing controller-runtime client from `setup()`.
- `setup/metrics/client.go` — if `--in-cluster-metrics`, GET Service `thanos-querier` in `openshift-monitoring` and use `https://<name>.<namespace>.svc:<port>`; else GET Route `prometheus-k8s`. Keep `InsecureSkipVerify: true`.
- `setup/test/fakeclient.go` — register `corev1.AddToScheme` so ConfigMap create works in unit tests.

**Depends on:** nothing in this plan.

**Tests:**

- `setup/results/results_test.go` (new) — fake client: omitted name → no ConfigMap; set name → ConfigMap `results.csv` matches file bytes; laptop file still created.
- `setup/metrics/client_test.go` or table in `gather_test.go` — `--in-cluster-metrics` uses Service lookup; unset still uses Route (Route GET can stay untested if it needs a Route object).

**Done when:** `make test-setup` passes; `go run setup/main.go --help` shows `--results-configmap` and `--in-cluster-metrics`; omitting them still writes `tmp/results/` only. **Merged to `master` before any real Test Run Jobs.**

### Phase 1 — Job image and entrypoint

**Goal:** `build/perf-job/Dockerfile` plus a shell entrypoint that can run `make dev-deploy-latest` / `setup` from a baked checkout with `USE_INSTALLED_KSCTL=true`.

**Changes:**

- `build/perf-job/Dockerfile` — UBI; `oc`; Go 1.26.x matching `go.mod`; install `ksctl` `@master` the same way `make/ksctl.mk` `get-ksctl-and-install` does; `COPY` repo; `go build -o /usr/local/bin/setup ./setup`.
- `build/perf-job/entrypoint.sh` — kubeconfig from SA, `cd` to baked tree, `export USE_INSTALLED_KSCTL=true`, dispatch DeploySandbox vs setup argv (setup Jobs include `--in-cluster-metrics` and `--results-configmap`).
- Makefile — `perf-job-image` wrapping `podman`/`docker` build for local iteration.

**Depends on:** Phase 0 on the git ref the BuildConfig clones (`master` after PR 1). Local Dockerfile work can start in parallel; do not run a real Test Run until that merge.

**Tests:** No cluster in CI. A local `podman build` of `build/perf-job` is enough to merge. Optional: a tiny script test that entrypoint refuses unknown phase. Throwaway-cluster BuildConfig proof is a Phase 6 README checklist.

**Done when:** Image builds locally; entrypoint documents `USE_INSTALLED_KSCTL=true` on the DeploySandbox path.

### Phase 2 — Test Run record, validation, Prepare

**Goal:** HTTP `POST /runs` (or the library it calls) validates kubeconfig/SAR/setupRuns/template, writes app ConfigMap+Secret, ensures test ns/SA/CRB, starts the Git-source build, records `buildName`/`imageTag`, returns 201 — or sets `Failed` in the same request if start-build fails.

**Changes:**

- `perfapp/main.go` — `net/http` listen `127.0.0.1:8080` (proxy in deploy phase).
- `perfapp/run/` — Test Run JSON, labels `app=onboarding-perfapp`, `testrun=<id>`, `testhost=<sha256>`; overlap list; deterministic Job names.
- `perfapp/validate/` or under `perfapp/` — kubeconfig load, SAR, `TransformUsername`, `GetTemplateFromContent`, custom ≤ users.
- `perfapp/remote/` — ensure `sandbox-perf-test`, SA `setup-runner`, cluster-admin CRB, BuildConfig/ImageStream, start-build. Use `github.com/openshift/api/build/v1` (already in `go.mod` via `openshift/api`).
- `make/test.mk` — `test-perfapp` (`go test .../perfapp/...`) added to `make test` in this same PR (`test-support test-setup test-perfapp`).
- `perfapp/run/*_test.go`, `perfapp/remote/*_test.go` — fake clients (`setup/test` pattern + corev1 + build).

**Depends on:** Phase 0 merged to the ref BuildConfig clones if Prepare is tested end-to-end; fakes do not.

**Tests:** Design list: bad kubeconfig, SAR deny, empty setupRuns, duplicate username, `TransformUsername` reject, custom > users, missing template if any custom > 0, non-Template YAML, all-custom-0 without file; overlap 409 vs different API host; start-build failure → ConfigMap `Failed` not 201.

**Done when:** Those tests pass under `make test`; Prepare records Build name before 201 on the success path.

### Phase 3 — Poller and Job chain

**Goal:** Ticker (+ startup) advances `Prepare` → `DeploySandbox` → `SetupRunning` (index 0..N-1) → `Succeeded`/`Failed`; GET-before-create; copy `setup-run-results-<i>` → app key `results-<i>.csv`.

**Changes:**

- `perfapp/run/poller.go` (name can match repo style) — one transition per tick after ConfigMap update.
- `perfapp/remote/jobs.go` — create Job `deploy-sandbox-<id>` / `setup-<i>-<id>`; resources per design; mount templates ConfigMap when custom > 0; argv from `setupRuns[i]` including `--in-cluster-metrics` and `--results-configmap=setup-run-results-<i>`.
- `perfapp/remote/results.go` — GET test ConfigMap, patch app ConfigMap data.

**Depends on:** Phases 1–2 (image tag + Prepare Build Complete).

**Tests:** DeploySandbox success creates `setup-0` only if missing; that Job Failed → no `setup-1`; last setup success → `Succeeded`; Prepare Complete with DeploySandbox already present → no second Job; teardown without CSV → `Failed`; CSV copy on setup success.

**Done when:** Fake-client phase machine tests pass under `make test`; poller does not create duplicate deterministic Jobs.

### Phase 4 — HTML

**Goal:** Operators can submit the form, list runs, and download CSVs without a SPA.

**Changes:**

- `perfapp/templates/` — `html/template` for `GET /`, `GET /runs`, `GET /runs/<id>`.
- Form: default two README rows; add/remove; template required in UI when any custom > 0.
- `perfapp/http_test.go` — handler tests with `httptest` (validation errors, list/detail from ConfigMaps).

**Depends on:** Phase 2 (create) and Phase 3 (status/CSV keys) for a useful detail page; can stub ConfigMaps for HTML-only tests.

**Done when:** Default form posts two presets; detail shows `deploySandbox` + each `setupRuns[i]` and `results-<i>.csv` download.

### Phase 5 — Deploy on the app cluster

**Goal:** `deploy/onboarding-perfapp/` runs replicas 1, oauth2-proxy sidecar on 4180, app on localhost 8080, Service exposes only the proxy.

**Changes:**

- `deploy/onboarding-perfapp/` — Namespace, RBAC (ConfigMaps/Secrets in app ns only), Deployment, Service, Route, Secret placeholders for GitHub OAuth. **oauth2-proxy image pinned by digest.** **perfapp image tag is the git SHA the Makefile pushes**, not `:latest`.
- `build/perfapp/Dockerfile` — multi-stage compile `perfapp`.
- Makefile — `perfapp-build`, `perfapp-image` (tag = git SHA).

**Depends on:** Phases 2–4 for a binary worth deploying.

**Tests:** Document that unauthenticated traffic never hits `:8080` if the Service is correct. Optional e2e later; not CI.

**Done when:** YAML applies on an app cluster with filled OAuth Secret; `replicas: 1`; proxy digest and perfapp SHA tag are in the manifests.

### Phase 6 — Docs

**Goal:** `perfapp/README.md` covers deploy, GitHub OAuth Secret, form prereq (onboarding operator already installed), that Jobs clone nothing, and a throwaway-cluster checklist for Git-source BuildConfig + first Test Run.

**Changes:** `perfapp/README.md` only — do not rewrite `setup/README.md`. Point laptop users at existing setup README; mention `--results-configmap` and `--in-cluster-metrics`.

**Depends on:** Phase 5 YAML names/flags.

**Done when:** README is enough to deploy and run one Test Run against a throwaway test cluster, including the BuildConfig proof that is not a CI/merge gate.

## Dependency graph

```
PR 1: Phase 0 (setup CLI)  ──must merge to master before real Jobs──┐
                                                                     │
Phase 1 (job image) ──► Phase 3 (poller/Jobs) ──► Phase 4 (HTML)     │
         ▲                     ▲                    ▲                │
         │                     │                    │                │
Phase 2 (validate/Prepare) ────┘                    │                │
         │                                          │                │
         └──────────────► Phase 5 (deploy) ─────────┴──► Phase 6     │
                              (oauth2 digest, SHA tag)   (README     │
                                                          + cluster  │
                                                          checklist) │
```

Phase 0 is a **separate PR**. After it is on `master`, Phase 1 (Dockerfile) and Phase 2 (`perfapp/` types + `make test` wiring) can proceed in parallel. HTML (4) can start against fake ConfigMaps in parallel with poller (3) once JSON shape is stable.

## Test strategy

- **Unit (CI):** GitHub Actions `unit-tests` runs `make test`. Phase 0 uses existing `test-setup`. The first `perfapp/` PR adds `test-perfapp` so `make test` is `test-support test-setup test-perfapp`.
- **When tests land:** same PR as the code they lock (Phases 0, 2, 3, 4).
- **Not in CI / not a merge gate:** live BuildConfig, `make dev-deploy-latest`, 2k users, oauth2-proxy GitHub login. Local `podman build` of `build/perf-job` is the Phase 1 merge bar.

Compatible with later `/implement-with-tasks`: each phase’s **Tests** / **Done when** become task checkboxes.

## Migration / rollout

- **CLI:** additive flags `--results-configmap` and `--in-cluster-metrics`; default behavior unchanged. Ship in PR 1 before any Test Run Jobs.
- **perfapp:** new deploy; no data migration. First install creates empty app namespace.
- **Feature flags:** none in v1; app is either deployed or not.
- **Order:** merge phase 0 to `master` → job image + perfapp PRs → deploy YAML with oauth2-proxy digest and perfapp SHA tag → README cluster checklist before the first real Test Run.

## Risks

| Risk | Mitigation |
| --- | --- |
| BuildConfig clones `master` without phase 0 | PR 1 first; do not run real Test Runs until that ref has the flags |
| `make ksctl` reclones despite baked binary | Image installs `ksctl` at build time; entrypoint `USE_INSTALLED_KSCTL=true` |
| `ksctl` `@master` drifts between image builds | Accept (Q5); rebuild the job image when DeploySandbox breaks |
| GOPROXY blocked on test cluster | Image build fails in Prepare; operator sees Failed Build; README checklist |
| ConfigMap 1MiB if many CSVs | v1 summary CSVs are small; split later if needed |
| Two poller replicas | `replicas: 1` + deterministic Job names |
| oauth2-proxy CVE | Bump digest in deploy YAML |

## Out of scope

- `setup start` / in-cluster laptop CLI.
- Least-privilege Job SA.
- Test-cluster teardown / `make clean-users` from the UI.
- Live 2k CI.
- Rewriting `setup/README.md`.
- Metrics URL env var (`METRICS_URL`) or `--metrics-url` string.

## Handoff to tasks

`/implement-with-tasks` should turn these into `onboarding-perf-webapp-tasks.md`:

1. **PR 1:** ConfigMap results writer + unit tests (`setup/results`, `setup/cmd`, fake client scheme).
2. **PR 1:** `--in-cluster-metrics` Service lookup + unit tests (`setup/metrics/client.go`, `setup/cmd/root.go`).
3. `build/perf-job/Dockerfile` (`ksctl` `@master` at image build) + `entrypoint.sh` + `USE_INSTALLED_KSCTL=true`; local `podman build` is the merge bar.
4. `perfapp/run` types, overlap, labels, tests; wire `test-perfapp` into `make test`.
5. Submit validation (kubeconfig, SAR, setupRuns, template, `TransformUsername`).
6. `perfapp/remote` Prepare (ns/SA/CRB/BuildConfig/start-build) + Failed-on-error.
7. Poller + Job create (`--in-cluster-metrics`, `--results-configmap`) + CSV copy + tests.
8. HTML form/list/detail.
9. `build/perfapp/Dockerfile` + `deploy/onboarding-perfapp/` (oauth2-proxy **digest**, perfapp **git SHA** tag) + Makefile targets.
10. `perfapp/README.md` including throwaway-cluster BuildConfig checklist.

## Decisions

| # | Decision |
| --- | --- |
| Q1 | Phase 0 is its own PR; then `perfapp/` PRs |
| Q2 | Local `podman build` to merge the job image; throwaway-cluster BuildConfig is a README checklist |
| Q3 | `make test` includes `test-perfapp` in the first `perfapp/` PR |
| Q4 | `--in-cluster-metrics` boolean; setup looks up Service `thanos-querier` (not a URL on argv) |
| Q5 | Install `ksctl` `@master` at job-image **build** time; Jobs set `USE_INSTALLED_KSCTL=true` |
| Q6 | oauth2-proxy pinned by digest; perfapp image tagged with git SHA, not `:latest` |
| (rename) | Go package and paths are `perfapp/`, not `webapp/` |
