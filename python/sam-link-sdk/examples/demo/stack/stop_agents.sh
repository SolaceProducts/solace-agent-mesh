#!/usr/bin/env bash
# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

# Stops the demo agents started by start_agents.sh.
AGENTS="examples/demo/(claims_intake_langchain|policy_desk_adk|regulations_hermes|fraud_screen_crewai|records_lookup_strands|repair_estimator_langgraph|parts_pricing_openai|settlement_review_maf).py"
pkill -f "$AGENTS" || true
sleep 1
pkill -9 -f "$AGENTS" 2>/dev/null || true
echo "demo agents stopped"
