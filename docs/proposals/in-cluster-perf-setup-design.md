# In-cluster performance setup — design

**Status:** Final

**Sketch:** [in-cluster-perf-setup-sketch.md](in-cluster-perf-setup-sketch.md)
**Decisions:** [in-cluster-perf-setup-design-questions.md](in-cluster-perf-setup-design-questions.md) · sketch: [in-cluster-perf-setup-questions.md](in-cluster-perf-setup-questions.md)

## Overview

The performance `setup` tool today is a long-running local process (`setup/cmd/root.go`, `go run setup/main.go`). This design splits it into:

- a **controller** CLI on the laptop (`start`, `status`, `results`, `stop`)
- a **runner** that executes the existing provisioning/metrics flow as a Kubernetes **Job** (`setup run`)

so a laptop sleep or VPN drop cannot abort a 2k-user run.

## Design Principles

1. **The Job is the run.** Local process lifetime is unrelated to test lifetime. Ctrl-C, a dropped watch, and `os.Exit` on the laptop must not delete the Job.
2. **One execution path.** There is no in-process `--local` mode. The runner in the Job is the only place provisioning happens.
3. **The checkout on disk is the runner.** `start` uploads the local tree with `oc start-build --from-dir`; OpenShift builds it into an ImageStream so the Job cannot silently run a stale published image.
4. **Reuse the current flow.** Do not change user-provisioning concurrency, operator templates, metric queries, or CSV columns. Change *where* they run and *how* they are driven.
5. **Cluster objects stay simple.** No CRD. Generated Job names, singleton status/results ConfigMaps, one namespace. `cluster-admin` on the runner SA is accepted for dedicated perf clusters.
6. **Fail closed on overlap.** A second `start` while a Job is active is an error, not a queue.

## Architecture / How It Works

```
 Laptop (controller)                         Cluster (ns: sandbox-perf-test)
┌─────────────────────────────┐
│ setup start                 │
│  validate flags/templates   │
│  ensure ns/SA/CRB           │
│  oc start-build --from-dir  │──────► BuildConfig → ImageStream
│  templates ConfigMap        │──────► setup-run-templates
│  create Job + args          │──────► Job setup-run-<timestamp>
│  prompt: wait or detach     │          command: setup run --users ...
│ setup status | results | stop
└─────────────────────────────┘              │
                                             ▼
                                    Pod (SA setup-runner, cluster-admin)
                                    setup run <flags>
                                      │
                    ┌─────────────────┼──────────────────┐
                    ▼                 ▼                  ▼
              K8s API           thanos-querier      status/results
              (users, ops,      :9091 + SA token    ConfigMaps
               templates)
```

### Start sequence

1. Operator is logged in (`oc login`), same as today. `oc` is required (`start-build`).
2. `setup start <flags>` validates the same constraints as today's `setup()` (user counts, templates exist, username prefix, workloads pairs, operators-limit).
3. If `--interactive` (default), confirm the target cluster and template list **before** any cluster writes.
4. Using the existing kubeconfig client (`configuration.NewClient`):
   - List Jobs labeled `app=setup-run` in `sandbox-perf-test`. If any is active, exit with an error pointing at `setup status` / `setup stop`.
   - Ensure namespace `sandbox-perf-test`, ServiceAccount `setup-runner`, ClusterRoleBinding to `cluster-admin`, and a Docker-strategy BuildConfig outputting to ImageStream `setup-runner`.
   - Unless `--skip-build`: `oc start-build --from-dir <module-root> --follow` with a **unique** ImageStream tag for this invocation. With `--skip-build`, print the last tag (from the previous Job spec or ImageStream annotation) and fail if none exists.
   - If ConfigMap `setup-run-results` exists and is not annotated as retrieved: warn. Interactive: confirm or abort. `--interactive=false`: warn and continue. Then delete the ConfigMap.
   - Reset status ConfigMap to `Pending`.
   - If `--template` files were given, write ConfigMap `setup-run-templates` and plan mount path `/templates/<basename>`.
   - Create Job `setup-run-<timestamp>` labeled `app=setup-run`. Container command is `setup run` plus the same run flags as `start` (not `--kubeconfig`, `--interactive`, `--wait`, `--skip-build`). Rewrite `--template` args to the mount paths. Image: `image-registry.openshift-image-registry.svc:5000/sandbox-perf-test/setup-runner:<tag>`. `imagePullPolicy: IfNotPresent`. `restartPolicy: Never`, `backoffLimit: 0`. No `activeDeadlineSeconds`. No `ttlSecondsAfterFinished`.
5. Print Job name, namespace, and later commands. If waiting, also print `oc logs -n sandbox-perf-test -f job/<name>`.
6. If `--interactive`: prompt to wait, stating the Job is already running independently and naming `setup status` / `setup results`. If yes, poll the status ConfigMap until complete or the watch is dropped. If `--interactive=false`, do not wait unless `--wait`.
7. A dropped watch prints that the Job is still running and how to check it; it does not delete the Job.

### Runner sequence

`setup run` is hidden from `--help`. If invoked outside a pod (`rest.InClusterConfig` fails / no SA token file), it exits with a clear error.

The runner is the body of today's `setup()` minus kubeconfig lookup, `oc whoami -t`, interactive prompts, TTY progress bars, and local `tmp/results/` as the source of truth.

1. Parse the same cobra flags as `start` (values come from the Job argv).
2. Create an **in-cluster** controller-runtime client (`rest.InClusterConfig()`), same scheme as today plus `corev1` (ConfigMaps). Keep QPS/Burst 100.
3. Read the projected SA token for Prometheus; do not call `auth.GetTokenFromOC`.
4. Point the Prometheus API client at `https://thanos-querier.openshift-monitoring.svc:9091` with `Authorization: Bearer <SA token>`. Keep the existing insecure TLS skip.
5. Execute the unchanged pipeline: verify sandbox operators → configure space tier / disable copied CSVs → install operators from **image-baked** relative paths → provision users / idlers / templates (custom paths under `/templates`) → gather metrics → optional 15-minute settle.
6. On a ticker (and on phase changes), write the status ConfigMap. User-work goroutines keep in-process counters; those counts are copied into status instead of `uiprogress`.
7. On success (and on fatal, via the existing pre-exit hook), write the results CSV into ConfigMap `setup-run-results` (no `retrieved-at` yet). Logs stay on the pod.
8. Exit 0 or 1; Job `Succeeded`/`Failed` follows. The Job is not retried.

Relative paths `setup/resources/user-workloads.yaml` and `setup/operators/installtemplates/...` resolve because the image `WORKDIR` is the module-root equivalent and those directories were `COPY`'d in.

### Status / results / stop

- **`setup status`** lists Jobs with `app=setup-run`: prefer the **active** one, else the most recently completed. Reads the status ConfigMap. Prints phase, counts, start time, last error, Job condition, Job name, and whether results exist (and whether they were retrieved). If nothing is present, say so.
- **`setup results`** GETs `setup-run-results`. If missing, fail clearly and point at `oc logs` for the relevant Job. If present, write `tmp/results/<timestamp><testname>.csv`, print the table, and annotate the ConfigMap as retrieved (`retrieved-at`).
- **`setup stop`** deletes only the **active** Job. It does not delete users, operators, older Jobs, or `make clean-users` resources. Best-effort: set status phase to `Stopped`. After stop, a new `start` is allowed.

Wait mode is the same status printer in a loop, plus the one-time logs hint. It does not follow logs.

### Cluster objects

Namespace: **`sandbox-perf-test`** (fixed; no flag).

| Object | Role |
| --- | --- |
| ServiceAccount `setup-runner` | Job identity |
| ClusterRoleBinding `setup-runner-admin` | `cluster-admin` → that SA |
| BuildConfig `setup-runner` | Docker strategy, binary source, output ImageStream |
| ImageStream `setup-runner` | Tags per `start`; last-used tag recorded for `--skip-build` |
| ConfigMap `setup-run-templates` | Custom template file contents (keys = basenames), mounted at `/templates` |
| ConfigMap `setup-run-status` | Structured status JSON (current run) |
| ConfigMap `setup-run-results` | `results.csv`; deleted at next `start` after the retrieval warning |
| Job `setup-run-<timestamp>` | One-shot runner; label `app=setup-run` |

Job image (in-cluster):

`image-registry.openshift-image-registry.svc:5000/sandbox-perf-test/setup-runner:<tag>`

Controller uses the Go client for namespace/SA/CRB/Job/ConfigMaps. Shell out to **`oc start-build --from-dir`** (and `--follow`) for the image; that is the supported way to upload a local build context. No podman, no registry default route.

## Core Concepts

### Controller vs runner

Same Go module, same `setup` binary.

| Command | Where | Visible |
| --- | --- | --- |
| `setup start` | laptop | yes |
| `setup status` | laptop | yes |
| `setup results` | laptop | yes |
| `setup stop` | laptop | yes |
| `setup run` | Job only | hidden |

Laptop-only flags on `start`: `--kubeconfig`, `--interactive`, `--wait`, `--skip-build`, `-v`. Those are not copied onto the Job.

### Run flags on the Job

`start` copies onto `setup run`:

- `--username`, `--users`, `--default`, `--custom`
- `--host-ns`, `--member-ns`
- `--skip-wait`, `--skip-idler`, `--skip-install-operators`, `--operators-limit`, `--idler-timeout`
- `--testname`, `--workloads`
- `--template` rewritten to `/templates/<basename>`

### Status document

JSON in `setup-run-status` data key `status.json`:

```json
{
  "phase": "ProvisioningUsers",
  "jobName": "setup-run-20260826-190000",
  "imageTag": "20260826-190000",
  "startedAt": "2026-08-26T19:00:00Z",
  "usersSignedUp": 420,
  "idlersUpdated": 400,
  "defaultTemplatesApplied": 100,
  "customTemplatesApplied": 50,
  "lastError": ""
}
```

Phases: `Pending` | `InstallingOperators` | `ProvisioningUsers` | `Settling` | `Succeeded` | `Failed` | `Stopped`.

The runner is the writer during the run. The controller writes `Pending` on start, `Stopped` on stop.

### Results document

ConfigMap data key `results.csv` — the same CSV bytes `results.Results` writes today. Annotation (or label) `retrieved-at` is set only by `setup results` after a successful local write. Absence of that annotation is what triggers the `start` warning.

### Metrics client

- URL: `https://thanos-querier.openshift-monitoring.svc:9091`
- Token: `/var/run/secrets/kubernetes.io/serviceaccount/token`
- TLS: keep `InsecureSkipVerify` unless we later load the service CA

Queries themselves are unchanged.

### Image

Dockerfile at `build/setup/Dockerfile` (same `build/` convention as other images in this repo): multi-stage UBI build of the `setup` binary, `COPY setup/resources` and `setup/operators`, `WORKDIR` at the module-root layout. No `oc` in the image.

BuildConfig in `sandbox-perf-test` uses Docker strategy and binary input. Each `start` (without `--skip-build`) starts a build to ImageStreamTag `setup-runner:<unique>`. Unique tag format: timestamp is enough (`20060102-150405`).

## Implementation Plan

### Package layout

Keep `setup/main.go`. Split the 487-line `cmd` package:

| Path | Responsibility |
| --- | --- |
| `setup/cmd/root.go` | Cobra root `setup`, persistent `--kubeconfig` / `-v`, register subcommands |
| `setup/cmd/start.go` | `start` flags (today's flags + `--wait` + `--skip-build`), validation, prompts, orchestrate ensure/build/submit/optional wait |
| `setup/cmd/status.go` | `status` |
| `setup/cmd/results.go` | `results` |
| `setup/cmd/stop.go` | `stop` |
| `setup/cmd/run.go` | Hidden `run`; fail if not in-cluster; invoke `setup/run` |
| `setup/run/` | Extracted pipeline from current `setup()` |
| `setup/cluster/` | Ensure ns/SA/CRB/BuildConfig, Job list/create/delete, ConfigMaps, "is a run active?" |
| `setup/image/` | Wrap `oc start-build --from-dir` (unique tag, `--follow`, last-tag for `--skip-build`) |
| `setup/configuration` | Add `NewInClusterClient`; add `corev1`/`batchv1`/`rbac`/`build` types to scheme as needed |
| `setup/metrics` | In-cluster Thanos URL + SA token |
| `setup/results` | ConfigMap writer for the runner; CSV encoding unchanged |

Existing packages (`users`, `idlers`, `operators`, `resources`, `wait`, `templates`) stay as-is aside from custom template paths coming from `/templates`.

### `configuration.NewClient`

- Controller: kubeconfig (`--kubeconfig` / `$KUBECONFIG` / `~/.kube/config`)
- Runner: `rest.InClusterConfig()`

### Progress without a TTY

Replace `uiprogress` in the runner with counters. A goroutine every few seconds (and at phase change) marshals them into the status ConfigMap. Wait mode renders that ConfigMap as text.

### Fatal handling

`terminal.Fatalf` already runs pre-exit hooks. The runner registers a hook that writes `phase=Failed`, `lastError=...`, and whatever results exist. Job then exits non-zero.

### Tests

Fake-client style (`setup/test.NewFakeClient`):

- `cluster`: ensure ns/SA/CRB; reject `start` when a Job is active; pick active vs latest completed; results retrieval annotation and delete-on-start warning path
- `run`: flags including rewritten `--template` paths
- `metrics`: client constructed with token + Thanos URL does not hit the Route API
- CLI: no-args errors toward `start`; `--help` lists start/status/results/stop and hides `run`
- Keep operator/user/resource tests unchanged

`oc start-build` is wrapped so tests can assert args without a real cluster.

### Docs

Update `setup/README.md`: `go run setup/main.go start ...`, prereqs (`oc` login; no podman), `sandbox-perf-test`, status/results/stop, `--skip-build`, retrieve results before the next start, laptop may disconnect after start.

Old `go run setup/main.go --users ...` (no subcommand) errors with a pointer to `start`.

### Rollout order

1. Dockerfile + BuildConfig helpers + `setup/image` (`oc start-build`).
2. `setup/cluster` objects and Job create/list.
3. Extract `setup/run`; cobra `run` / `start` / `status` / `results` / `stop`.
4. Status ConfigMap + results ConfigMap + retrieval annotation.
5. Metrics → Thanos + SA token.
6. README + unit tests.

## Decisions (index)

| # | Decision |
| --- | --- |
| Q1 | Hidden `setup run`; fail outside a pod |
| Q2 | Job argv is `setup run` + cobra flags; templates ConfigMap for file contents |
| Q3 | `oc start-build --from-dir` (no podman) |
| Q4 | Unique ImageStream tag per start |
| Q5 | Rebuild by default; `--skip-build` reuses last tag |
| Q6 | Job name `setup-run-<timestamp>`; at most one active; keep old Jobs |
| Q7 | Skipped (no laptop registry push) |
| Q8 | Wait polls status; print `oc logs` once |
| Q9 | `COPY` templates; module-root `WORKDIR` |
| Q10 | Namespace `sandbox-perf-test` |
| Q11 | No `activeDeadlineSeconds` |
| Q12 | Delete results at next `start`; warn/confirm if not retrieved |
