# In-cluster performance setup — sketch questions

**Status:** Resolved — all sketch questions decided
**Related:** [Sketch document](in-cluster-perf-setup-sketch.md)

Sketch updated from these decisions. Detailed architecture belongs in a design-with-questions session.

## Q1: What cluster object runs the test?

The runner must survive laptop disconnect, report completion, and not be a long-lived always-on service. The choice sets the mental model for start/status/results.

### Option A: Kubernetes Job
- **Pro:** Built for one-shot work; the API already has Succeeded/Failed, start/completion times, and a pod to hold logs. Status mapping is straightforward.
- **Con:** Jobs are slightly heavier than a Pod (completions, backoff). A default `OnFailure` restart could re-run a destructive setup; backoff and restart policy must be chosen carefully.

**Decision:** Option A — Kubernetes Job is the native one-shot primitive and gives Succeeded/Failed, timestamps, and pod logs.

_Considered and rejected: Option B (bare Pod — no first-class completion, easy to lose on eviction), Option C (Deployment — always-on watcher is the wrong shape for an occasional hour-long test)_

## Q2: How is the local CLI split across start, status, and results?

The user wants three distinct uses. Cobra already owns the `setup` command; this decides how those uses show up.

### Option A: Subcommands (`setup start`, `setup status`, `setup results`)
- **Pro:** Matches the three uses directly. Start keeps today's flags; status/results stay flag-light. Discoverable via `--help`.
- **Con:** Existing docs and muscle memory (`go run setup/main.go --users 2000 ...`) have to change to `... start --users 2000 ...`.

**Decision:** Option A — the three uses are three commands (`setup start`, `setup status`, `setup results`).

_Considered and rejected: Option B (mode flags mixed into the start command — easy to misuse), Option C (separate binaries — extra build/docs cost for one tool)_

## Q3: Does `start` wait for the run, or return as soon as the Job exists?

The whole point is that the laptop may go away. Whether start blocks anyway affects UX and whether people keep a terminal attached "just in case."

### Option D: Prompt after submit — wait or detach
- **Pro:** Operator chooses per run. The prompt can state clearly that the Job already exists and will keep running, and can print `setup status` (and `setup results`) so disconnect does not look like failure. Matches today's `--interactive` confirm-before-act pattern.
- **Con:** One more prompt on every start. Non-interactive/scripted runs need a default (no wait unless `--wait`).

**Decision:** Option D — after the Job is created, prompt whether to wait. The prompt must say the Job is already running independently and name the commands to check later (`setup status`, `setup results`). `--interactive=false` skips the prompt and does not wait, unless `--wait` is passed (for scripts that want to block). Ctrl-C / a dropped watch must not delete the Job.

_Considered and rejected: Option A (always detach — no chance to watch without a second command), Option B (`--wait` only — no prompt, easy to miss that status exists), Option C (always wait — dropped watch still looks like the command failed)_

## Q4: Keep a local-only execution mode?

Cluster-side is the goal. Local execution is still useful when iterating on the tool itself (no image rebuild, `--skip-wait`, fast `--users 1` checks).

### Option A: Cluster-only; remove in-process execution
- **Pro:** One path to test and document. No drift between local and in-cluster behavior (kubeconfig vs in-cluster config, token vs SA, files vs ConfigMaps).
- **Con:** Inner-loop development needs an image build/push for every change. Debugging the runner is heavier.

**Decision:** Option A — cluster-only. The long-running setup always executes as a Job; do not keep an in-process `--local` path.

_Considered and rejected: Option B (cluster default + `--local` — two execution paths to keep in sync), Option C (local default + `--cluster` — onboarding can still hit laptop-sleep failures)_

## Q5: Where does the runner image come from?

The Job needs a container that contains the setup binary and built-in templates. Kickoff UX depends on whether start also builds an image.

### Option D: Build locally, push to the OpenShift internal registry
- **Pro:** The Job runs the exact local checkout (important now that there is no `--local` path). No quay publish step. Uses the cluster already under test; ImageStream + internal registry is a normal OpenShift workflow.
- **Con:** `start` needs podman/docker (or equivalent) and registry login. Kickoff includes a build/push, so it is slower than pointing at a published image. Internal registry route/credentials must work from the laptop.

**Decision:** Option D — build the runner image from the local tree and push it to the cluster's OpenShift internal registry. The Job references that image so the run matches the checkout. `start` is responsible for getting the image there before creating the Job.

_Considered and rejected: Option A (published quay image — can lag the git checkout), Option B (always build/push, but destination was unspecified / not internal registry), Option C (`make` + `--image` required — extra step, easy to run a stale image)_

## Q6: How does the runner publish status for `setup status`?

The controller should not scrape unstructured logs as the primary interface, but logs remain useful for debugging.

### Option A: ConfigMap (or annotated Job) with structured fields
- **Pro:** Easy to read from the controller; no CRD. Fine for coarse phase + counts. Survives pod restart if the runner updates it periodically.
- **Con:** Eventual consistency; a crash between updates can show stale progress. ConfigMap size is not an issue for status.

**Decision:** Option A — structured status in a ConfigMap (Job annotations optional). Logs stay the detailed trace, not the status API.

_Considered and rejected: Option B (custom resource — CRD is heavy for a one-Job tool), Option C (pod logs only — fragile parsing, truncation on long runs)_

## Q7: Where are results stored until `setup results` fetches them?

Today's CSV is small. Logs can be larger. Retrieval should still produce a local `tmp/results/*.csv`.

### Option A: Results ConfigMap (CSV text) + Job pod logs for stdout/stderr
- **Pro:** No PVC. CSV is well under 1Mi. Controller can `Get` the ConfigMap and write the file. Logs stay with the Job.
- **Con:** ConfigMap 1Mi limit is a hard ceiling if we later dump huge artifacts. Failed Jobs that never write the ConfigMap need a clear error.

**Decision:** Option A — CSV in a results ConfigMap; stdout/stderr via Job pod logs. `setup results` writes the CSV under `tmp/results/` as today.

_Considered and rejected: Option B (PVC — overkill for a ~35-row CSV), Option C (parse CSV from stdout — brittle, the tool already redirects stdout because of this)_

## Q8: How does the in-cluster runner authenticate to Prometheus?

Today the laptop uses `oc whoami -t` against the Prometheus *route*. That is exactly what dies when the laptop sleeps, and tokens expire mid-run.

### Option A: ServiceAccount + in-cluster Thanos/Prometheus service
- **Pro:** No user token, no route, no laptop. Standard OpenShift pattern (`cluster-monitoring-view`). Survives the whole Job.
- **Con:** Requires a ClusterRoleBinding. In-cluster URL/TLS differs from today's route client (small code change in the metrics client).

**Decision:** Option A — in-cluster scrape with the Job ServiceAccount. No user OAuth token and no Prometheus route.

_Considered and rejected: Option B (copy user token into a Secret — still expires, still the external route), Option C (gather metrics later from the laptop — loses sampling over the full run)_

## Q9: How do custom `--template` files reach the runner?

Default templates can live in the image. Custom onboarding templates are local files at kickoff.

### Option A: Controller creates a ConfigMap from local files; Job mounts it
- **Pro:** Matches `--template path` UX. No image rebuild. ConfigMap is enough for typical OpenShift templates.
- **Con:** 1Mi ConfigMap limit (usually fine). Need a convention for mount path vs flag values inside the Job.

**Decision:** Option A — at kickoff, put custom template files in a ConfigMap and mount it on the Job. Built-in templates stay in the image.

_Considered and rejected: Option B (bake custom templates into the image — breaks pass-a-file-path), Option C (inline YAML on the Job spec — ugly and size-limited)_

## Q10: Can more than one run be active?

A run fills the cluster with users and operators. Status/results are simpler if there is at most one current test.

### Option A: Single active run; `start` fails if one is already running
- **Pro:** Matches reality (one cluster, one load test). Status/results have an obvious target. No naming scheme for operators to remember.
- **Con:** Cannot queue a second experiment without waiting or cancelling.

**Decision:** Option A — at most one active run. `start` fails if a Job is already running. `--testname` remains a results-file suffix, not a concurrent-run id. After a run completes, a new `start` is allowed.

_Considered and rejected: Option B (named concurrent runs — overlapping tests would corrupt metrics and user counts)_

## Q11: Where does the Job run, and how broad is its RBAC?

The runner installs cluster operators, creates UserSignups, updates ToolchainConfig and OLMConfig, and queries monitoring. That is far beyond a namespaced Role.

### Option B: Dedicated namespace + `cluster-admin` on the Job SA
- **Pro:** These are dedicated perf clusters (`kubeadmin` already). No mid-run RBAC surprises. Fastest to ship.
- **Con:** Blunt. A bug in the runner is fully privileged. Slightly worse hygiene.

**Decision:** Option B — Job lives in a dedicated namespace; its ServiceAccount is bound to `cluster-admin`. Enumerating a least-privilege ClusterRole is not worth the mid-run miss risk on a throwaway perf cluster.

_Considered and rejected: Option A (purpose-built ClusterRole — wide anyway, easy to miss a verb and fail mid-run), Option C (run in `toolchain-host-operator` with the host-operator SA — couples load-test permissions to the operator)_

## Q12: Does the local CLI include a stop/cancel command?

Not in the three stated uses. A 2k-user Job that was started with the wrong flags will otherwise have to be deleted by hand with `oc`.

### Option A: Yes — `setup stop` deletes/interrupts the current Job
- **Pro:** Completes the controller story (start/status/results/stop). Safer than asking people to `oc delete job` and hoping they got the right one.
- **Con:** Extra command to document. Stop does not undo provisioned users (same as killing the local process today).

**Decision:** Option A — `setup stop` interrupts the current Job. It does not clean up provisioned users; that stays `make clean-users`.

_Considered and rejected: Option B (operators delete the Job with `oc` — easy to delete the wrong object; status can look confusing)_
