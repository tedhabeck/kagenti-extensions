#!/bin/bash
# Verify that a permissive callee serves both TLS and plaintext on
# the same port. We hit it with plain HTTP from a curl pod (no
# authbridge sidecar, no SPIRE) and assert the request succeeds.
#
# Usage: verify-permissive.sh <namespace> <callee>

set -euo pipefail

NAMESPACE=${1:?usage: verify-permissive.sh <namespace> <callee>}
CALLEE=${2:?}

echo "[*] Hitting $CALLEE with plain HTTP from a non-mesh client"
out=$(kubectl -n "$NAMESPACE" run --rm -i --restart=Never \
        --image=curlimages/curl:latest \
        --command "verify-permissive-$$" -- \
        curl -sS --max-time 10 \
             "http://${CALLEE}.${NAMESPACE}.svc.cluster.local:8080/healthz" 2>&1) || {
  echo "ERROR: plaintext request to permissive callee failed:" >&2
  echo "$out" >&2
  exit 1
}
echo "  Response: $out"
echo
echo "============================================================"
echo " Permissive verified — plaintext callers served on same port"
echo "============================================================"
