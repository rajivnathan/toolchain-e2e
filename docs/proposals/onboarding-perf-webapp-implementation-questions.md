# Onboarding performance web app — implementation questions

**Status:** Resolved — all landing decisions recorded  
**Related:** [Implementation plan](onboarding-perf-webapp-implementation-plan.md) · [Design](onboarding-perf-webapp-design.md)

Landing decisions are recorded below. The plan is ready for `/implement-with-tasks`.

## Q1: Phase 0 as its own PR?

Jobs on the test cluster run `setup` from the Git-source checkout (`master` once the BuildConfig is used). The ConfigMap CSV writer and in-cluster metrics lookup are the only changes that existing `setup/` tests and laptop users will see. Splitting them from `perfapp/` changes how soon a real Test Run can succeed and how large the first review is.

### Option A: Separate PR for phase 0, then perfapp PRs
- **Pro:** `make test-setup` stays the review surface; laptop users get flags without deploying an app; BuildConfig can clone `master` as soon as that PR merges.
- **Con:** Two (or more) merges before a working UI; flag names must stay stable for later Job argv.

**Recommendation:** Option A. Phase 0 is independently useful and is the merge gate for Git-source Jobs.

**Decision:** Option A — phase 0 merges on its own so Git-source Jobs can clone `master` with the new flags before any `perfapp/` code lands.

_Considered and rejected: Option B (one PR with CLI plus perfapp would delay Jobs on `master` and mix setup review with HTTP/poller)._

## Q2: When to require a live OpenShift proof of the Git-source image?

Prepare’s BuildConfig + ImageStream + start-build cannot be fully faked in CI. Waiting for a throwaway cluster before writing the poller delays the rest of the stack; shipping the Dockerfile unproven risks first-run failures in Prepare.

### Option B: Local `podman build` is enough to merge; throwaway-cluster BuildConfig is a Phase 6 / first-run checklist
- **Pro:** Image and perfapp work proceed without waiting for hardware; CI stays unit tests.
- **Con:** First real Test Run may fail in Prepare (GOPROXY, permissions, BuildConfig YAML).

**Recommendation:** Option B. The design already excludes live BuildConfig from CI. Treat cluster proof as a README checklist, not a merge gate.

**Decision:** Option B — merge on a local image build; prove BuildConfig on a throwaway cluster as a first-run checklist, not a PR gate.

_Considered and rejected: Option A (cluster proof as merge-gate blocks landing when no cluster is available), Option C (deferring the Dockerfile until just before deploy lets Job argv and entrypoint drift)._

## Q3: Wire `go test ./perfapp/...` into `make test`?

Today `make test` is `test-support` + `test-setup` (`make/test.mk`) — the unit-test target GitHub Actions `unit-tests` runs, not `make test-e2e`. The HTTP app lives under `perfapp/`. That package must either join `make test` or stay a separate `make test-perfapp` that CI might miss.

### Option A: Add `test-perfapp` to `make test` in the same PR that adds `perfapp/`
- **Pro:** CI cannot skip the controller; matches how `setup` is tested.
- **Con:** `make test` gets slower as HTTP/poller tests grow.

**Recommendation:** Option A. The perfapp is the new product; it should fail the same `make test` as `setup`.

**Decision:** Option A — `make test` becomes `test-support test-setup test-perfapp` in the first `perfapp/` PR so GitHub Actions unit-tests run `go test .../perfapp/...`.

_Considered and rejected: Option B (a separate `make test-perfapp` would be easy for CI to skip)._

## Q4: How do Jobs tell `setup` to use in-cluster Prometheus?

Laptop `setup` GETs Route `prometheus-k8s`. A Job should use Thanos in-cluster instead. Passing the URL on argv duplicates knowledge `setup` can look up the same way it already looks up the Route.

### Option A: Cobra flag only (e.g. `--metrics-url`); empty means Route lookup
- **Pro:** Visible in Job spec next to `--users`; easy to unit-test `root.go` flag wiring; no hidden env.
- **Con:** Entrypoint must always append the flag for in-cluster Jobs; Jobs hardcode a Thanos URL that can drift from the cluster.

**Recommendation:** Option A. Jobs already pass a long argv (`--users`, `--results-configmap`); keep metrics on that list.

**Decision:** Option A — Jobs pass `--metrics-url https://thanos-querier.openshift-monitoring.svc:9091`; empty flag keeps the laptop Route lookup.

_Considered and rejected: Option B (env-only hides the value from Job argv), Option C (flag+env fallback is two ways to set the same thing)._

**Revision:** Boolean `--in-cluster-metrics` instead of a URL. Unset = today’s Route `prometheus-k8s` GET. Set = GET Service `thanos-querier` in `openshift-monitoring` and use `https://<name>.<namespace>.svc:<port>`. Jobs pass `--in-cluster-metrics`. Setup owns both discovery paths.

_Considered and rejected (revision): explicit `--metrics-url` (Jobs should not hardcode the Thanos hostname)._

## Q5: How should the job image install `ksctl`?

`make/ksctl.mk` clones `ksctl@master` unless `USE_INSTALLED_KSCTL=true`. The image must put a binary on `PATH` so DeploySandbox does not clone. Pinning vs following `master` is a landing choice.

### Option B: Install `@master` (or latest release) at image **build** time, same as `get-ksctl-and-install`
- **Pro:** Matches current make defaults; no extra pin to maintain.
- **Con:** Two image builds of the same Dockerfile can ship different `ksctl` binaries.

**Recommendation:** Option A. Test Runs already take hours; an unexpected ksctl change is hard to debug. Pin and bump deliberately.

**Decision:** Option B — install `ksctl` the same way `make/ksctl.mk` does (`@master` at image build time); DeploySandbox still uses `USE_INSTALLED_KSCTL=true` so Jobs do not clone again.

_Considered and rejected: Option A (pinning a commit would require Dockerfile bumps whenever make defaults move)._

## Q6: Pin container images in deploy YAML by digest?

`deploy/onboarding-perfapp` will reference a perfapp image and `oauth2-proxy`. Floating tags can change under a running Deployment.

### Option A: Pin oauth2-proxy (and, when published, the perfapp image) by digest
- **Pro:** Matches a cautious dashboard-style deploy; replayable installs.
- **Con:** Every proxy CVE bump is a YAML edit.

**Recommendation:** Option A for oauth2-proxy (third-party). For the perfapp image, pin the tag the Makefile pushes (e.g. git SHA) rather than `:latest`.

**Decision:** Pin oauth2-proxy by digest. Pin the perfapp image to the git SHA tag the Makefile pushes, not `:latest` (and not necessarily an image digest).

_Considered and rejected: Option B (floating `:latest` / `:v1` for both images)._
