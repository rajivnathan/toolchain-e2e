# In-cluster performance setup — design questions

**Status:** Resolved — all design questions decided
**Related:** [Design document](in-cluster-perf-setup-design.md)

Design updated from these decisions. Sketch decisions live in [in-cluster-perf-setup-questions.md](in-cluster-perf-setup-questions.md) and were not reopened here.

Sketch decisions live in [in-cluster-perf-setup-questions.md](in-cluster-perf-setup-questions.md) and are not reopened here.

## Q1: How is the runner started inside the Job?

The container needs a stable command. The same binary is the controller on the laptop.

### Option A: Hidden subcommand `setup run`
- **Pro:** Explicit; Job `command: ["setup", "run"]` is obvious in `oc get job -o yaml`. Cannot be confused with `start`. Easy to hide from `--help`.
- **Con:** Another subcommand to maintain. Accidental local `setup run` would fail without in-cluster config (must error clearly).

**Decision:** Option A — Job runs `setup run`. Fail fast if `run` is invoked outside a pod.

_Considered and rejected: Option B (autodetect via `KUBERNETES_SERVICE_HOST` — same argv means different things on laptop vs pod; env can be set locally)_

## Q2: How does `start` pass flags to the runner?

The Job cannot see laptop flags unless we serialize them.

### Option B: Container args (`setup run --users 2000 --default 2000 ...`)
- **Pro:** Runner reuses cobra flag parsing. Less new code.
- **Con:** Job spec becomes a long argv. Custom template *contents* still need a ConfigMap, so we have two mechanisms anyway. Harder to inspect as a whole than a ConfigMap.

**Decision:** Option B — `start` copies the run flags onto the Job as `setup run --users ...` (same cobra flags as `start`, minus laptop-only ones). Custom `--template` files still go in a ConfigMap and are mounted; args point at those mount paths.

_Considered and rejected: Option A (JSON ConfigMap for flags — extra parse path when cobra already has the flags), Option C (ConfigMap + `--config` path — extra wiring)_

## Q3: What builds the image on the laptop?

The runner image must come from the local checkout and land in this cluster's internal registry.

### Option C: `oc start-build --from-dir` (build on cluster)
- **Pro:** No local podman; no registry defaultRoute from the laptop. `oc` is already a prereq. Image lands in an ImageStream in the dedicated namespace.
- **Con:** Diverges from the sketch's "build locally with podman and push." Build logs live on the BuildConfig. Needs a Docker-strategy BuildConfig and the Dockerfile in the uploaded context. First start is still several minutes (cluster compile).

**Decision:** Option C — `start` uploads the local tree with `oc start-build --from-dir` (binary Docker strategy) so OpenShift builds the image into the internal registry/ImageStream. Laptop does not run `podman build` or `podman push`. This supersedes the sketch's local-build-and-push path; the checkout is still the source.

_Considered and rejected: Option A (podman then docker), Option B (podman only — extra prereq and registry route)_

## Q4: How is the runner image tagged?

The Job must pull what `start` just built. Retagging `:latest` can race with a stale node cache.

### Option A: Unique tag per `start` (timestamp or random suffix)
- **Pro:** Each Job pins an immutable tag. No doubt the pod has *this* build. `imagePullPolicy: IfNotPresent` is safe.
- **Con:** ImageStream accumulates tags (acceptable on a throwaway perf cluster). Status/Job YAML shows a noisy tag.

**Decision:** Option A — each `start` builds an ImageStream tag unique to that invocation; the Job image reference includes that tag.

_Considered and rejected: Option B (`:latest` + Always pull — simpler name, easier to run a cached/stale image)_

## Q5: Must every `start` rebuild the image?

Cluster-only means even `--users 1` needs an image. Rebuilding when only flags changed is slow.

### Option B: Always rebuild by default; `--skip-build` reuses the last pushed tag
- **Pro:** Fast reruns when only flags change. Default rebuild means you do not run old code unless you opt in.
- **Con:** `--skip-build` after a code change *does* run stale code. Must print the tag being reused.

**Decision:** Option B — rebuild on every `start` unless `--skip-build` is set. `--skip-build` must print the ImageStream tag it will reuse and fail if none exists yet.

_Considered and rejected: Option A (always rebuild — punishing for flag-only retries), Option C (mtime vs ImageStream — unreliable)_

## Q6: How do we identify the current Job, and what happens to a finished one?

One active run. `status`/`results`/`stop` need a single target. A completed Job still occupies the name if we use a fixed name.

### Option B: Generated names (`setup-run-<timestamp>`) + label `app=setup-run`; at most one *active*
- **Pro:** Previous Job/logs remain. `oc get jobs` is a history.
- **Con:** Status must List and pick "the" run (latest? only active?). Completed jobs accumulate. Stop must target the active one only.

**Decision:** Option B — Jobs are named `setup-run-<timestamp>` and labeled. At most one Job may be **active**; `start` fails if one is. `status` prefers the active Job, otherwise the most recently completed. `stop` deletes only the active Job. Older Jobs/logs remain until someone deletes them.

_Considered and rejected: Option A (fixed name `setup-run` — next start deletes the previous Job and its pod logs)_

## Q7: The internal registry is not exposed off-cluster by default. Should `start` expose it?

Push from the laptop would need Route `default-route` in `openshift-image-registry`.

**Skipped:** Moot. Q3 chose `oc start-build --from-dir`, so the image is built on the cluster into an ImageStream. `start` does not `podman push` and does not need the registry default route.

_Would have considered: Option A (auto-set `defaultRoute: true`), Option B (require the route and fail with the patch command)._

## Q8: What does the optional wait after `start` show?

The operator may choose to watch. The Job is independent of that watch.

### Option C: Status poll plus a hint to `oc logs -n ... -c ...` for detail
- **Pro:** Clean wait UX; detail is one documented command away.
- **Con:** Two places to look.

**Decision:** Option C — wait polls the status ConfigMap (phase + counts) and prints the `oc logs` command once when wait starts. Do not follow logs in the wait loop. A dropped watch must say the Job is still running.

_Considered and rejected: Option A (status poll only — no pointer to logs), Option B (follow pod logs — dropped follow looks like failure)_

## Q9: How do built-in templates get into the runner image?

Runner code uses filesystem paths (`setup/resources/user-workloads.yaml`, `setup/operators/installtemplates/...`). Custom templates are a separate ConfigMap.

### Option A: COPY those directories into the image; `WORKDIR` such that the same relative paths work
- **Pro:** Minimal code change. Tests that read files from disk stay valid on the laptop. Matches how the binary is run from the module root today.
- **Con:** Image must be built from the module root. Paths break if WORKDIR is wrong.

**Decision:** Option A — Dockerfile copies `setup/resources` and `setup/operators`; image `WORKDIR` is the module-root equivalent so existing relative paths keep working. `oc start-build --from-dir` uploads that context from the repo root.

_Considered and rejected: Option B (`go:embed` — extra call-site churn for little gain on the first cut)_

## Q10: Is the dedicated namespace a fixed name or a flag?

Sketch example was `toolchain-perf-setup`; the chosen name is `sandbox-perf-test`.

### Option A: Fixed namespace name
- **Pro:** Status/results/stop need no extra flag. Docs and muscle memory are one name. `oc -n sandbox-perf-test get job,cm`.
- **Con:** Cannot park the Job elsewhere without a code change.

**Decision:** Option A — always use namespace `sandbox-perf-test`. No `--setup-ns` flag.

_Considered and rejected: Option B (override flag — every command would have to take it; easy to pass on start and forget on status)_

## Q11: Should the Job have a time limit?

A 2k run is ~1h plus operator installs (RHOAI up to 15m) plus a 15m settle. A hung API wait could last forever (`DefaultTimeout` is per-wait, but the overall process has no cap).

### Option A: No `activeDeadlineSeconds`
- **Pro:** A slow cluster cannot be killed mid-settle. Failure modes stay "runner exits" not "kubelet SIGKILL."
- **Con:** A truly stuck Job sits until `setup stop`.

**Decision:** Option A — no Job-level deadline. Per-resource waits still time out via `wait.For*`. Use `setup stop` if the run is stuck.

_Considered and rejected: Option B (e.g. 6h `activeDeadlineSeconds` — can SIGKILL a legitimate slow run)_

## Q12: When a new `start` replaces a previous run, what happens to the old CSV?

Results live in ConfigMap `setup-run-results`. A new run will eventually overwrite them.

### Option B: Delete the results ConfigMap at the beginning of `start`
- **Pro:** No stale CSV. `setup results` during a run says "not ready."
- **Con:** Starting the next experiment before copying the file off-cluster loses the previous CSV.

**Decision:** Option B with a retrieval guard — `start` deletes `setup-run-results` so a new run cannot be confused with old numbers. `setup results` annotates the ConfigMap after a successful local write (e.g. `retrieved-at`). If the ConfigMap exists and is **not** annotated, `start` warns that results have not been downloaded. Interactive mode prompts to confirm before delete; declining aborts `start`. `--interactive=false` prints the warning and deletes (scripts must run `setup results` first if they care).

_Considered and rejected: Option A (leave old CSV until the new runner writes — status and results can disagree about which run they describe)_
