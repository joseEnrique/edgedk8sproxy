#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./k8s/deploy-agent.sh /path/agent.crt /path/agent.key /path/server-ca.crt [namespace]
# Defaults:
#   namespace = multiplexer-agent
# Requires:
#   - kubectl configured to point to the target cluster
#   - k8s/agent.yaml present (already committed)

AGENT_CRT=${1:-}
AGENT_KEY=${2:-}
SERVER_CA=${3:-}
NS=${4:-multiplexer-agent}

if [[ -z "${AGENT_CRT}" || -z "${AGENT_KEY}" || -z "${SERVER_CA}" ]]; then
  echo "Usage: $0 /path/agent.crt /path/agent.key /path/server-ca.crt [namespace]" >&2
  exit 1
fi

if [[ ! -f "${AGENT_CRT}" ]]; then
  echo "❌ agent.crt not found: ${AGENT_CRT}" >&2
  exit 1
fi
if [[ ! -f "${AGENT_KEY}" ]]; then
  echo "❌ agent.key not found: ${AGENT_KEY}" >&2
  exit 1
fi
if [[ ! -f "${SERVER_CA}" ]]; then
  echo "❌ server-ca.crt not found: ${SERVER_CA}" >&2
  exit 1
fi

echo "📦 Ensuring namespace ${NS} exists..."
kubectl get ns "${NS}" >/dev/null 2>&1 || kubectl create ns "${NS}"

echo "🔐 Creating/Updating Secret multiplexer-agent-certs in ${NS}..."
kubectl -n "${NS}" create secret generic multiplexer-agent-certs \
  --from-file=agent.crt="${AGENT_CRT}" \
  --from-file=agent.key="${AGENT_KEY}" \
  --from-file=server-ca.crt="${SERVER_CA}" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "🚀 Applying Deployment from k8s/agent.yaml..."
kubectl apply -f "$(dirname "$0")/agent.yaml"

echo "✅ Done. Check status with: kubectl -n ${NS} get pods"
