#!/bin/bash
# Patch the operator-rendered authbridge config ConfigMap to add an
# mtls block at the top level. Idempotent — re-running with the same
# mode produces no change.
#
# Usage: patch-mtls-config.sh <namespace> <agent-name> <mode>
#   mode is "permissive" or "strict"

set -euo pipefail

NAMESPACE=${1:?usage: patch-mtls-config.sh <namespace> <agent-name> <mode>}
AGENT=${2:?usage: patch-mtls-config.sh <namespace> <agent-name> <mode>}
MODE=${3:?usage: patch-mtls-config.sh <namespace> <agent-name> <mode>}

case "$MODE" in
  permissive|strict) ;;
  *) echo "ERROR: mode must be permissive or strict, got: $MODE" >&2; exit 1 ;;
esac

CM="authbridge-config-${AGENT}"
HERE=$(dirname "$0")

echo "[*] Patching $CM in $NAMESPACE with mtls.mode=$MODE"

# Pull the current rendered config, merge in the mtls block, write
# back. We stream the YAML through the python merger rather than try
# to do it in shell because the operator's config has nested
# pipeline / listener blocks that we shouldn't touch.
ORIG=$(kubectl -n "$NAMESPACE" get cm "$CM" -o jsonpath='{.data.config\.yaml}')
if [[ -z "$ORIG" ]]; then
  echo "ERROR: ConfigMap $CM has no config.yaml entry. Operator hasn't rendered it yet?" >&2
  exit 1
fi

MERGED=$(echo "$ORIG" | python3 "$HERE/mtls-merge.py" "$MODE")

# kubectl apply with a fresh ConfigMap manifest (preserves labels /
# ownership; --dry-run=client + apply is the standard idempotent
# pattern).
kubectl -n "$NAMESPACE" create configmap "$CM" \
  --from-literal=config.yaml="$MERGED" \
  --dry-run=client -o yaml | \
  kubectl -n "$NAMESPACE" apply -f - >/dev/null

echo "[*] Patched."
