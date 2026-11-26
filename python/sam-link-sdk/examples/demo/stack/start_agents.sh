#!/usr/bin/env bash
# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

# Starts the demo agents in the background, each joined to SAM's agent link with its own token (link_tokens.sh);
# logs go to DEMO_LOG_DIR. Needs the model settings in the environment (examples/demo/common.py) and SAM_LINK_URL,
# the entrypoint's connect URL (ws://<sam host>:<port>/gw/agent-link/v1/connect).
#
#   uv sync --all-extras
#   examples/demo/stack/start_agents.sh              # all agents
#   examples/demo/stack/start_agents.sh policy_desk_adk parts_pricing_openai
#
# Regulations (Hermes Agent) needs Python 3.14 in an environment of its own (docs/frameworks/hermes.md): it starts
# only when HERMES_PYTHON names that environment's interpreter.
# PYTHON is the command that runs the others (default: uv run --all-extras python, from python/sam-link-sdk).
# DEMO_OTLP_ENDPOINT exports OpenTelemetry metrics to an OTLP/HTTP endpoint (otel-collector.yaml receives them).
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../../.." && pwd)
LOGS=${DEMO_LOG_DIR:-${TMPDIR:-/tmp}/sam-demo}
TOKENS=${DEMO_TOKEN_DIR:-$HERE/tokens}
PYTHON=${PYTHON:-uv run --all-extras python}
mkdir -p "$LOGS"
: "${SAM_LINK_URL:?set SAM_LINK_URL to the entrypoint connect URL, ws://<sam host>:<port>/gw/agent-link/v1/connect}"
export SAM_LINK_URL

agent_name() {
  case "$1" in
    claims_intake_langchain) echo claims-intake ;;
    policy_desk_adk) echo policy-desk ;;
    regulations_hermes) echo regulations ;;
    fraud_screen_crewai) echo fraud-screen ;;
    records_lookup_strands) echo records-lookup ;;
    repair_estimator_langgraph) echo repair-estimator ;;
    parts_pricing_openai) echo parts-pricing ;;
    settlement_review_maf) echo settlement-review ;;
    *) echo "unknown demo agent: $1" >&2; return 1 ;;
  esac
}

if [[ $# -eq 0 ]]; then
  set -- claims_intake_langchain policy_desk_adk fraud_screen_crewai records_lookup_strands \
    repair_estimator_langgraph parts_pricing_openai settlement_review_maf
  [[ -n "${HERMES_PYTHON:-}" ]] && set -- "$@" regulations_hermes
fi
for file in "$@"; do
  name=$(agent_name "$file")
  token="$TOKENS/$name.link-token"
  [[ -f "$token" ]] || { echo "no link token for $name: run $HERE/link_tokens.sh" >&2; exit 1; }
  python=$PYTHON
  extra=()
  if [[ $file == regulations_hermes ]]; then
    python=${HERMES_PYTHON:?set HERMES_PYTHON to the interpreter of the Hermes environment}
    extra=(env "HERMES_HOME=${HERMES_HOME:-$ROOT/.hermes-regulations}")
  fi
  pkill -f "examples/demo/$file.py" 2>/dev/null || true
  (cd "$ROOT" && SAM_LINK_TOKEN=$(cat "$token") nohup ${extra[@]+"${extra[@]}"} $python "examples/demo/$file.py" > "$LOGS/$file.log" 2>&1 &)
  echo "started $file as $name (log $LOGS/$file.log)"
done
