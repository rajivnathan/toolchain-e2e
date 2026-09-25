# Onboarding performance app (perfapp)

A Go HTTP service that runs the Dev Sandbox operator-onboarding performance procedure against a test cluster you provide. Laptop use of the `setup` CLI is unchanged; see [`setup/README.md`](../setup/README.md).

Jobs pass `--results-configmap` and `--in-cluster-metrics` (added in phase 0). Laptop runs omit those flags and still write `tmp/results/*.csv`.

## Deploy on the app cluster

1. Create a GitHub OAuth App (see [GitHub OAuth](#github-oauth) below). The callback host should be the OpenShift Route (`oc get route perfapp -n onboarding-perfapp`); you can edit the callback URL after the Route exists.
2. Generate a cookie secret (`python3 -c 'import os,base64; print(base64.urlsafe_b64encode(os.urandom(32)).decode())'`).
3. Edit `deploy/onboarding-perfapp/secret.yaml` (`client-id`, `client-secret`, `cookie-secret`, `github-org`, optional `github-team`).
4. Build and push the perfapp image tagged with the git SHA (`QUAY_NAMESPACE` is your Quay username; see [`quay.md`](../quay.md)):

   ```
   export QUAY_NAMESPACE=<your-quay-username>
   make perfapp-image
   podman push quay.io/${QUAY_NAMESPACE}/perfapp:$(git rev-parse HEAD)
   ```

   The image is `quay.io/${QUAY_NAMESPACE}/perfapp:<git-sha>`.
5. Apply the manifests with that image:

   ```
   make perfapp-deploy
   ```

   Or replace `QUAY_NAMESPACE` and `GIT_SHA` in `deploy/onboarding-perfapp/deployment.yaml` and run `oc apply -k deploy/onboarding-perfapp`.

The Deployment has **replicas: 1**. oauth2-proxy is a sidecar on port **4180**. The Service exposes **only** that port. The app listens on `127.0.0.1:8080` and is not on the Service, so unauthenticated traffic cannot skip the proxy.

oauth2-proxy is pinned by digest. Bump the digest in `deployment.yaml` when you take a proxy CVE fix.

## GitHub OAuth

The UI sits behind a GitHub oauth2-proxy sidecar. Create a GitHub **OAuth App** (not a GitHub App) and put the credentials in `deploy/onboarding-perfapp/secret.yaml`.

Do this on GitHub after the OpenShift Route exists so the callback host is known (`oc get route perfapp -n onboarding-perfapp`). You can edit the callback URL later if the host changes.

1. In the GitHub organization that should be allowed to use the UI (recommended) or in a personal account: **Settings → Developer settings → OAuth Apps → New OAuth App**.
2. Set:
   - **Application name** — any name operators will recognize (for example `onboarding-perfapp`).
   - **Homepage URL** — `https://<route-host>` (the Route host from the previous command).
   - **Authorization callback URL** — `https://<route-host>/oauth2/callback`. This must match exactly; oauth2-proxy uses `/oauth2/callback`.
   - Leave **Device Flow** disabled.
3. Register the app, copy the **Client ID**, and generate a **Client secret**. Put those values in `secret.yaml` as `client-id` and `client-secret`.
4. Set `github-org` in `secret.yaml` to that organization's login (the org slug). Only members of that org can sign in. oauth2-proxy requests the `read:org` scope for this check.
5. Optional: set `github-team` to one or more **team slugs** (not display names), comma-separated, to further restrict access within the org. Leave it empty to allow any org member.
6. If the organization has **OAuth App access restrictions** (Settings → Third-party Access), an org owner must approve this OAuth App. Until that happens, membership checks fail and users cannot sign in even with a valid Client ID.

The cookie secret in `secret.yaml` is not created on GitHub. Generate it locally as in the deploy steps above.

## Form prereq

The onboarding operator must **already be installed** on the test cluster. The app does not install Sandbox operators on the app cluster.

## What Jobs do

Prepare creates a Git-source BuildConfig on the **test** cluster (`sandbox-perf-test`) that builds `build/perf-job/Dockerfile`. That Dockerfile starts from `quay.io/jeevandroid/perf-job-base` (`oc` and Go are already installed), installs `ksctl` from `master`, then copies this repository and compiles `setup`. Phase Jobs pull the tag and run `make dev-deploy-latest` / `setup`. They **do not clone** this repo and **do not** compile `setup` again.

Rebuild and push the base image when those tools change: `make perf-job-base-image`, then `podman push quay.io/jeevandroid/perf-job-base`. Override the base with `--build-arg BASE_IMAGE=...`.

## Throwaway-cluster checklist (not a CI gate)

Use this before the first real Test Run (after phase 0 is on `master`):

1. `make perf-job-image` locally (`podman build` of `build/perf-job/Dockerfile`).
2. On a throwaway OpenShift cluster, create namespace `sandbox-perf-test`.
3. Create a Git-source Docker BuildConfig for `https://github.com/codeready-toolchain/toolchain-e2e` with `dockerfilePath: build/perf-job/Dockerfile`, output ImageStream `perf-job`.
4. Start a build. Confirm it can reach GitHub, pull `quay.io/jeevandroid/perf-job-base`, and compile `setup`.
5. Run a Job with that image, SA `setup-runner` + cluster-admin, args `deploy-sandbox`. Confirm `make dev-deploy-latest` and ToolchainStatus Ready.
6. Submit a 1-user Test Run from the UI against that cluster. Confirm setup Job args include `--in-cluster-metrics` and `--results-configmap=setup-run-results-0`, and that the CSV appears on the Test Run ConfigMap.

## Local development

```
go run ./perfapp --listen 127.0.0.1:8080 --namespace <your-ns>
```

Use a kubeconfig that can manage ConfigMaps/Secrets in that namespace. Point a browser at `http://127.0.0.1:8080`. GitHub user header will be `unknown` without oauth2-proxy.

By default the test-cluster BuildConfig clones `https://github.com/codeready-toolchain/toolchain-e2e` at `master`. To build a fork or branch instead:

```
go run ./perfapp --listen 127.0.0.1:8080 --namespace <your-ns> \
  --git-uri https://github.com/rajivnathan/toolchain-e2e \
  --git-ref my-branch
```

The fork must be reachable from the test cluster and contain `build/perf-job/Dockerfile`. The same flags work on the in-cluster Deployment if you add them to the container args.
