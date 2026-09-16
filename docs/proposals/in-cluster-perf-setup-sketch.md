# In-cluster performance setup

**Status:** Sketch complete — ready for detailed design

Decisions: [in-cluster-perf-setup-questions.md](in-cluster-perf-setup-questions.md)

## What this is

The `setup` command (`setup/cmd/root.go`, invoked via `go run setup/main.go`) is the Dev Sandbox performance-test tool. It provisions thousands of users, installs onboarded operators, applies user-workload templates, and gathers Prometheus metrics for roughly an hour (plus a 15-minute settle window).

Today that entire run executes on the operator's laptop. If the machine sleeps, loses VPN, or drops the API connection, the test stops or is left half-finished. Metrics gathering also depends on a user OAuth token and the external Prometheus route, both of which fail if the local session dies.

This sketch moves the **run** onto the cluster as a Kubernetes Job. The local program becomes a thin client:

1. **`setup start`** — kick off a test from a given configuration
2. **`setup status`** — check the current test
3. **`setup results`** — retrieve the CSV once the test has finished
4. **`setup stop`** — interrupt the current Job (does not delete provisioned users)

The laptop can then sleep or disconnect without affecting the test.

There is no in-process / `--local` execution path. Every run, including a one-user smoke test, goes through a Job.

## Problem

A typical 2k-user run is long-lived and chatty with the API. The current design couples that work to a local process:

| Concern | Today | After |
| --- | --- | --- |
| Process lifetime | Local `setup` process must stay up for the whole run | Job on the cluster; local CLI is optional after start |
| Connectivity | Every API call and Prometheus scrape goes through the laptop | Runner talks to the API and Prometheus in-cluster |
| Auth | `oc whoami -t` token for Prometheus; kubeconfig for the API | Job ServiceAccount (`cluster-admin` + monitoring access) |
| Progress | `uiprogress` bars in the local terminal | Status ConfigMap; optional local watch after a prompt |
| Artifacts | CSV + stdout/stderr files under `tmp/results/` on disk | CSV in a results ConfigMap; logs on the Job pod; `setup results` writes the CSV locally |
| Templates | Read from local filesystem paths | Built-in templates in the image; custom `--template` files uploaded as a ConfigMap |

The README already tells users to "grab some coffee" and to resume with a new username prefix if the run times out. Cluster-side execution removes the laptop-sleep class of failure. Manual resume with a new prefix stays as-is.

## How it relates to the existing system

The **work the tool does** does not change:

1. Validate configuration
2. Confirm sandbox host/member operators are installed
3. Configure default space tier and disable copied CSVs
4. Install additional operators and apply post-install templates
5. Provision users, wait for Spaces, optionally update Idlers
6. Apply default and custom user-workload templates
7. Sample Prometheus on an interval (cluster CPU/memory, etcd, OLM, API server, host/member operators, optional extra workloads)
8. Sleep 15 minutes after provisioning so metrics can settle
9. Write a results CSV (counts, timings, avg/max metrics)

What changes is **where** that work runs, and **how** a human drives it.

The local binary stays the entry point (`go run setup/main.go` / the `setup` CLI). It no longer performs steps 2–9 itself; it uploads the tree for an in-cluster image build, submits a Job, and later reads status and results back.

```
Today

  laptop  --(entire run)-->  API + Prometheus route
                              users, operators, templates, metrics

Proposed

  laptop  -- start/status/results/stop -->  API
              |                               |
              | oc start-build --from-dir     v
              +----->  ImageStream          Job (sandbox-perf-test)
                                              |
                    +-------------------------+-------------------------+
                    v                         v                         v
                   API              Prometheus (in-cluster)     status + results
                                                                ConfigMaps
```

## Key concepts

### Two roles, one binary family

- **Controller** — the local CLI (`setup start|status|results|stop`). Talks to the cluster only to start a build, create or delete the Job, and read ConfigMaps / logs. Safe to interrupt; interrupting it never deletes the Job.
- **Runner** — the existing setup logic, executing inside the Job with in-cluster credentials. Owns operator install, user provisioning, the settle window, and metrics gathering. Does not prompt and does not call `oc`.

The same checkout produces both: the controller runs locally via `go run`; the runner is that logic packaged into an image.

### Local CLI

| Command | Intent |
| --- | --- |
| `setup start` | Validate flags/templates, optionally confirm, ensure namespace/SA/RBAC, `oc start-build`, submit the Job, then prompt whether to wait |
| `setup status` | Show phase and coarse progress of the current run (or that none is running / last run finished) |
| `setup results` | Fetch the CSV ConfigMap and write `tmp/results/*.csv`; surface pod logs when useful |
| `setup stop` | Delete/interrupt the current Job. Does **not** clean up users (`make clean-users` still does that) |

`start` still accepts today's flags (`--users`, `--default`, `--custom`, `--template`, `--workloads`, `--testname`, `--idler-timeout`, skip flags, namespaces, and so on). Interactive confirm happens **locally**, before the Job is created. `--interactive=false` stays for scripts.

After the Job exists, an interactive prompt asks whether to wait. The prompt states that the Job is already running independently and names `setup status` and `setup results`. `--interactive=false` skips the prompt and does not wait unless `--wait` is passed. Ctrl-C or a dropped watch must not delete the Job, and the message must distinguish "watch ended" from "Job failed."

Existing invocations change from `go run setup/main.go --users 2000 ...` to `go run setup/main.go start --users 2000 ...`.

### In-cluster run

The run is a Kubernetes Job in the dedicated namespace `sandbox-perf-test`. Restart policy must not re-run a destructive setup on failure (treat as one-shot: no surprise second pass over users/operators).

The Job's ServiceAccount is bound to `cluster-admin`. These are dedicated perf clusters already used with `kubeadmin`; a least-privilege ClusterRole is not the goal.

At most one Job is **active**. Jobs are named `setup-run-<timestamp>` (previous Jobs and their logs are kept). `start` fails if a run is already in progress. `--testname` remains a results-file suffix, not a concurrent-run id. After a run completes (or is stopped), a new `start` is allowed.

The runner:

- Uses in-cluster config (ServiceAccount) instead of a kubeconfig file
- Does not prompt; configuration is already decided at kickoff
- Does not depend on `oc` or a user OAuth token
- Periodically writes structured status to a ConfigMap
- Writes the results CSV to a ConfigMap when it can; logs go to the pod

### Image

`start` uploads the local checkout with `oc start-build --from-dir` (Docker strategy BuildConfig). OpenShift compiles the image into an ImageStream in `sandbox-perf-test`. The Job references a **unique tag** for that start so the run matches the tree that was uploaded. Built-in templates (`setup/resources/user-workloads.yaml`, operator install/post-install YAMLs) ship in the image via `COPY` and a module-root `WORKDIR`. `--skip-build` reuses the last ImageStream tag.

Custom `--template` files are read on the laptop at kickoff, stored in a ConfigMap, and mounted into the Job. Passing a local path keeps working without baking onboarding templates into the image.

### Status

The runner publishes a status ConfigMap the controller reads. Logs are the detailed trace, not the status API.

Expected phases:

- `Pending` — submitted, not yet running
- `InstallingOperators`
- `ProvisioningUsers`
- `Settling` — post-provision metrics window
- `Succeeded` / `Failed`

Plus coarse counts (users signed up, default/custom templates applied, start time, last error). Terminal progress bars are not the in-cluster interface; if the operator chose to wait, the controller can render a local view from the ConfigMap.

### Results

The CSV is small (header + a few dozen metric rows). The runner writes it to a results ConfigMap. `setup results` fetches that ConfigMap, writes `tmp/results/` as today, and marks the ConfigMap as retrieved. A later `start` deletes the results ConfigMap so numbers cannot be confused with the new run; if they were never retrieved, `start` warns (and interactively confirms) before deleting.

Stdout/stderr stay on the Job pod (`oc logs` / `setup results` can show them). No PVC.

If the runner crashes before writing the ConfigMap, `setup results` must fail clearly and still point at pod logs.

### Metrics

The runner scrapes Prometheus (or Thanos querier) **in-cluster** using its ServiceAccount — not the Prometheus route, not `oc whoami -t`. That sampling continues if the laptop sleeps, and it does not depend on a user token that can expire mid-run.

`cluster-admin` on the Job SA is sufficient to query monitoring; the metrics client still needs an in-cluster URL/TLS path instead of the external route.

## Rough behavior

**Start**

1. Operator logs into the cluster as today (`oc login`).
2. Runs `setup start` with the same configuration flags as today.
3. Controller validates flags and local template files, optionally confirms.
4. If a Job is already running, fail with an instruction to use `setup status` or `setup stop`.
5. Controller ensures the dedicated namespace, ServiceAccount, and `cluster-admin` binding exist.
6. Controller uploads the local tree (`oc start-build --from-dir`) unless `--skip-build`, producing a unique ImageStream tag.
7. If a previous results ConfigMap exists and was never retrieved, warn (interactive: confirm). Then delete it.
8. Controller writes custom templates to a ConfigMap if needed, creates Job `setup-run-<timestamp>` with `setup run <flags>`, and prints enough identity to find it later.
9. If interactive: prompt to wait or detach, naming `setup status` and `setup results`. If waiting, poll status (and print the `oc logs` command once) until the Job finishes or the watch is dropped; dropping the watch does not stop the Job.
10. From this point the laptop is irrelevant to the run.

**Status**

1. Operator (possibly later, possibly from another machine with kubeconfig) runs `setup status`.
2. Controller reads the status ConfigMap and Job condition; reports phase, coarse counts, and whether results are ready.
3. If nothing is running and nothing completed recently, say so.

**Results**

1. After `Succeeded` (or `Failed`, if the CSV ConfigMap exists), `setup results` writes the file under `tmp/results/`.
2. Point at pod logs for debugging.

**Stop**

1. `setup stop` interrupts the current Job.
2. Provisioned users and installed operators remain; `make clean-users` is unchanged.

If the runner crashes, status shows `Failed` and results/logs are still retrievable when they exist. Resuming a partial user-provisioning run (today: new username prefix and remaining count) stays a **manual** re-start with flags; this sketch does not add automatic resume.

## What is out of scope

- Changing the provisioning algorithm, concurrency, operator list, or metric queries
- Automatic resume / checkpoint of a half-finished user-provisioning run
- A web UI or OpenShift console plugin
- Replacing the onboarding spreadsheet / baseline comparison process
- Running this as CI on every PR (it remains an operator-driven performance tool on a dedicated cluster)
- Cleaning up provisioned users (`make clean-users` stays as-is)
- Multi-cluster host/member split beyond what the tool already assumes (one host, one member)
- Publishing a quay image as the primary runner image
- A least-privilege ClusterRole for the Job SA
- Keeping an in-process local execution mode

## Success criteria

- A 2k-user run continues to completion if the laptop sleeps or disconnects after start
- Status and results remain available from any machine with cluster credentials
- Results CSV content remains comparable to today's output (same items, suitable for the onboarding spreadsheet)
- Custom `--template` files still work without baking them into the image
- `setup start` runs the checkout that was just uploaded and built on the cluster, not a stale published image
