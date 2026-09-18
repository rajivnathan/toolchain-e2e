# Onboarding performance web app — tasks

**Related:** [Implementation plan](onboarding-perf-webapp-implementation-plan.md) · [Design](onboarding-perf-webapp-design.md)

Checkboxes are the plan’s **Tests** / **Done when**. Tasks 1–2 are **PR 1** (phase 0 only). Do not run a real Test Run until those flags are on `master`.

## Task 1 — ConfigMap results writer (PR 1)

- [x] `setup/results` writes data key `results.csv` when `--results-configmap` is set
- [x] `setup/test` fake client scheme includes corev1; `configuration.NewScheme` can create ConfigMaps
- [x] Omitted name → no ConfigMap; set name → ConfigMap bytes match the file; laptop `tmp/results/` still created
- [x] `make test-setup` passes

## Task 2 — `--in-cluster-metrics` (PR 1)

- [x] Flag on `setup/cmd`; default false keeps Route `prometheus-k8s`
- [x] Set → GET Service `thanos-querier` in `openshift-monitoring`, URL `https://<name>.<ns>.svc:<port>`
- [x] Unit tests cover Service vs Route lookup
- [x] `go run setup/main.go --help` shows both new flags; `make test-setup` passes

## Task 3 — Job image

- [x] `build/perf-job/Dockerfile` (UBI, `oc`, Go 1.26.x, `ksctl` `@master` at image build, baked checkout, compiled `setup`)
- [x] `build/perf-job/entrypoint.sh` writes SA kubeconfig, `USE_INSTALLED_KSCTL=true`, dispatches deploy-sandbox vs setup; refuses unknown phase
- [x] `make perf-job-image` wraps local `podman`/`docker` build

## Task 4 — Test Run record + `make test`

- [x] `perfapp/run` types, labels `app=onboarding-perfapp` / `testrun` / `testhost`, deterministic Job names, overlap check
- [x] `make test` is `test-support test-setup test-perfapp`
- [x] Overlap: same API host while non-terminal → error; different host → allowed

## Task 5 — Submit validation

- [x] Bad kubeconfig, SAR deny, empty setupRuns, duplicate username, `TransformUsername` reject, custom > users
- [x] Missing template if any custom > 0; non-Template YAML; all-custom-0 without file succeeds

## Task 6 — Prepare on the test cluster

- [x] Ensure `sandbox-perf-test`, SA `setup-runner`, cluster-admin CRB, BuildConfig/ImageStream, start-build
- [x] Success records `buildName` / `imageTag` before 201
- [x] Start-build (or Ensure) failure → ConfigMap `Failed` + lastError, not 201

## Task 7 — Poller and Job chain

- [x] GET-before-create; DeploySandbox success creates `setup-0` only if missing; that Job Failed → no `setup-1`
- [x] Last setup success → `Succeeded`; Prepare Complete with DeploySandbox present → no second Job
- [x] Setup success copies `setup-run-results-<i>` → `results-<i>.csv`; teardown without CSV → `Failed`
- [x] Setup Jobs pass `--in-cluster-metrics` and `--results-configmap=setup-run-results-<i>`

## Task 8 — HTML

- [x] `GET /` default two README rows; add/remove; template required in UI when any custom > 0
- [x] `GET /runs`, `GET /runs/<id>` timeline + CSV download; `POST /runs` create
- [x] Handler tests (`httptest`) for validation errors and list/detail from ConfigMaps

## Task 9 — Deploy

- [x] `build/perfapp/Dockerfile`; `make perfapp-build` / `perfapp-image` (tag = git SHA)
- [x] `deploy/onboarding-perfapp/`: replicas 1, oauth2-proxy digest, Service exposes only :4180, app on 127.0.0.1:8080

## Task 10 — Docs

- [x] `perfapp/README.md`: deploy, OAuth Secret, operator already installed, Jobs do not clone, throwaway-cluster BuildConfig checklist
- [x] Point laptop users at `setup/README.md`; mention `--results-configmap` and `--in-cluster-metrics`
