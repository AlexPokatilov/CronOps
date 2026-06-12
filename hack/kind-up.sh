#!/usr/bin/env bash
# Spin up a local kind cluster and install the CronOps CRD.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-cronops-dev}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v kind >/dev/null; then
  echo "kind is not installed: https://kind.sigs.k8s.io/docs/user/quick-start/" >&2
  exit 1
fi

if ! kind get clusters | grep -qx "${CLUSTER_NAME}"; then
  kind create cluster --name "${CLUSTER_NAME}"
fi

kubectl apply -f "${REPO_ROOT}/deploy/crd/"

echo
echo "Cluster '${CLUSTER_NAME}' is ready. Next steps:"
echo "  go run ./cmd/controller --leader-elect=false   # terminal 1"
echo "  go run ./cmd/server --dev                      # terminal 2 (login admin/admin)"
echo "  cd web && npm run dev                          # terminal 3 -> http://localhost:5173"
echo "  kubectl apply -f examples/simple-ping.yaml"
