#!/usr/bin/env bash
# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

# Issues each demo agent's agent link token with `sam-link-sdk link-token`, one owner-only file per agent in
# tokens/ (never committed), and writes the SHA-256 digests SAM's entrypoint takes to tokens/digests.env
# (POLICY_DESK_TOKEN_SHA256=...), which agent_link.yaml reads. An agent that already has a token keeps it; remove its
# file to issue a new one. Tokens are never printed.
#
#   examples/demo/stack/link_tokens.sh
#
# DEMO_TOKEN_DIR moves the tokens elsewhere. SAM_LINK_SDK is the command that issues a token (default `uv run sam-link-sdk`,
# from python/sam-link-sdk; `sam-link-sdk` when the SDK is pip-installed).
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
TOKENS=${DEMO_TOKEN_DIR:-$HERE/tokens}
SAM_LINK_SDK=${SAM_LINK_SDK:-uv run sam-link-sdk}
AGENTS="claims-intake policy-desk regulations fraud-screen records-lookup repair-estimator parts-pricing settlement-review"

mkdir -p "$TOKENS"
chmod 700 "$TOKENS"
digests="$TOKENS/digests.env"
: > "$digests.new"
for agent in $AGENTS; do
  token="$TOKENS/$agent.link-token"
  if [[ -f "$token" ]]; then
    digest=$(shasum -a 256 < "$token" | cut -d' ' -f1)
    echo "kept   $agent"
  else
    digest=$($SAM_LINK_SDK link-token --out "$token")
    echo "issued $agent"
  fi
  echo "$(echo "$agent" | tr 'a-z-' 'A-Z_')_TOKEN_SHA256=$digest" >> "$digests.new"
done
mv "$digests.new" "$digests"
echo "digests for SAM's agent_link entrypoint: $digests"
