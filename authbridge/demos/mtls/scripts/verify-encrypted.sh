#!/bin/bash
# Verify that mTLS is active on both pods after deploy + restart.
#
# This is the "lightweight" check — confirms via authbridge logs that
# both sides are configured for mTLS, and confirms a caller→callee
# request succeeds. The full tcpdump-on-the-wire check is a separate
# manual verification step (see README) since it requires privileged
# pod access in the kind cluster.
#
# Usage: verify-encrypted.sh <namespace> <caller> <callee>

set -euo pipefail

NAMESPACE=${1:?usage: verify-encrypted.sh <namespace> <caller> <callee>}
CALLER=${2:?}
CALLEE=${3:?}

echo "[*] Confirming mTLS is enabled on both pods (via startup logs)"
for agent in "$CALLER" "$CALLEE"; do
  if ! kubectl -n "$NAMESPACE" logs deploy/"$agent" -c authbridge-proxy --tail=200 2>/dev/null | \
       grep -q '"mTLS enabled"'; then
    echo "  ERROR: $agent does not show 'mTLS enabled' in its authbridge logs." >&2
    echo "         Is the mtls block patched into its ConfigMap?" >&2
    exit 1
  fi
  mode=$(kubectl -n "$NAMESPACE" logs deploy/"$agent" -c authbridge-proxy --tail=200 2>/dev/null | \
         grep '"mTLS enabled"' | head -1 | sed -n 's/.*mode=\([a-z]*\).*/\1/p')
  echo "  $agent: mTLS enabled, mode=$mode"
done

echo
echo "[*] Triggering caller → callee request"
# The caller's outbound forward proxy is the path. We hit it from
# inside the caller pod with curl --proxy.
out=$(kubectl -n "$NAMESPACE" exec deploy/"$CALLER" -c demo-app -- \
        curl -sS --max-time 10 \
             -x http://localhost:8081 \
             "http://${CALLEE}.${NAMESPACE}.svc.cluster.local:8080/healthz" 2>&1) || {
  echo "ERROR: request failed:" >&2
  echo "$out" >&2
  exit 1
}
echo "  Response: $out"

echo
echo "[*] Confirming the wire was encrypted (via outbound forward-proxy logs)"
if kubectl -n "$NAMESPACE" logs deploy/"$CALLER" -c authbridge-proxy --tail=100 2>/dev/null | \
   grep -q "mtls fallback to plaintext.*${CALLEE}"; then
  echo "  ERROR: caller's forward proxy fell back to plaintext for ${CALLEE}." >&2
  echo "         This means strict mode is NOT actually encrypting the wire." >&2
  exit 1
fi
echo "  No fallback-to-plaintext WARN observed; wire is encrypted."

echo
echo "============================================="
echo " mTLS verified — caller → callee is encrypted"
echo "============================================="
