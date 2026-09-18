#!/usr/bin/env bash
set -euo pipefail

PHASE="${1:-}"
shift || true

case "${PHASE}" in
  deploy-sandbox|setup) ;;
  *)
    echo "unknown phase: ${PHASE}" >&2
    echo "usage: entrypoint deploy-sandbox | setup [setup flags...]" >&2
    exit 1
    ;;
esac

TOKEN_FILE="/var/run/secrets/kubernetes.io/serviceaccount/token"
CA_FILE="/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
NS_FILE="/var/run/secrets/kubernetes.io/serviceaccount/namespace"

TOKEN="$(cat "${TOKEN_FILE}")"
NS="$(cat "${NS_FILE}")"
export KUBECONFIG="${KUBECONFIG:-/tmp/kubeconfig}"

cat > "${KUBECONFIG}" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority: ${CA_FILE}
    server: https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT}
  name: in-cluster
contexts:
- context:
    cluster: in-cluster
    namespace: ${NS}
    user: sa
  name: in-cluster
current-context: in-cluster
users:
- name: sa
  user:
    token: ${TOKEN}
EOF

cd /opt/toolchain-e2e
export USE_INSTALLED_KSCTL=true
export PATH="/usr/local/bin:${PATH}"

case "${PHASE}" in
  deploy-sandbox)
    make dev-deploy-latest
    for _ in $(seq 1 180); do
      ready="$(oc get toolchainstatus toolchain-status -n toolchain-host-operator -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)"
      if [[ "${ready}" == "True" ]]; then
        exit 0
      fi
      sleep 10
    done
    echo "timeout waiting for ToolchainStatus Ready" >&2
    exit 1
    ;;
  setup)
    exec setup --token "${TOKEN}" "$@"
    ;;
esac
