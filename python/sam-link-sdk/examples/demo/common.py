# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

"""What the demo agents share: the made-up claims data, the model settings and the mesh settings.

Eight agents, one per framework, hand one claim to each other:

    claims-intake (LangChain) ─┬─ policy-desk (Google ADK) ───── regulations (Hermes Agent)
                               ├─ fraud-screen (CrewAI) ──────── records-lookup (Strands Agents)
                               ├─ repair-estimator (LangGraph) ─ parts-pricing (OpenAI Agents SDK)
                               └─ settlement-review (Microsoft Agent Framework)

Every name, address and number is made up.

Environment:

    SAM_LINK_URL, SAM_LINK_TOKEN    the agent link entrypoint and this agent's token (docs/agent-link.md)
    LLM_SERVICE_ENDPOINT, LLM_SERVICE_API_KEY, LLM_SERVICE_GENERAL_MODEL_NAME
                                    the model; ADK and CrewAI call it through LiteLLM, so its name carries the
                                    provider ("anthropic/claude-sonnet-5", "openai/<model>")
    DEMO_OPENAI_BASE_URL, DEMO_OPENAI_MODEL
                                    optional: an OpenAI-compatible endpoint and model name for the agents that do not
                                    use LiteLLM (for Anthropic: https://api.anthropic.com/v1/ and claude-sonnet-5)
    DEMO_OTLP_ENDPOINT              optional: an OTLP/HTTP endpoint to export OpenTelemetry metrics to
"""

from __future__ import annotations

import logging
import os
from typing import Any

POLICIES = {
    "P-1182": {
        "holder": "Dana Whitfield",
        "product": "home",
        "tier": "gold",
        "status": "active",
        "deductible_usd": 1000,
        "limit_usd": 50000,
        "covers": ["water damage", "fire", "theft", "storm"],
        "start_date": "2019-04-02",
    },
    "P-2040": {
        "holder": "Jordan Chen",
        "product": "auto",
        "tier": "silver",
        "status": "active",
        "deductible_usd": 500,
        "limit_usd": 25000,
        "covers": ["collision", "theft", "glass"],
        "start_date": "2024-11-15",
    },
    "P-3301": {
        "holder": "Sam Okafor",
        "product": "home",
        "tier": "bronze",
        "status": "lapsed",
        "deductible_usd": 2500,
        "limit_usd": 30000,
        "covers": ["fire"],
        "start_date": "2025-09-28",
    },
}

CLAIMS = {
    "C-7781": {
        "policy_id": "P-1182",
        "loss_type": "water damage",
        "amount_usd": 8400,
        "filed": "2026-10-10",
        "summary": "Burst pipe under the kitchen sink; flooring and lower cabinets replaced.",
    },
    "C-7790": {
        "policy_id": "P-2040",
        "loss_type": "collision",
        "amount_usd": 3150,
        "filed": "2026-10-11",
        "summary": "Rear-ended at a stop light; bumper and tail light replaced.",
    },
    "C-7802": {
        "policy_id": "P-3301",
        "loss_type": "storm",
        "amount_usd": 14900,
        "filed": "2026-10-12",
        "summary": "Roof damage after a storm; policy lapsed last month.",
    },
}

CLAIM_HISTORY = {
    "P-1182": [{"claim_id": "C-5120", "loss_type": "theft", "amount_usd": 1200, "filed": "2023-06-18"}],
    "P-2040": [],
    "P-3301": [
        {"claim_id": "C-7655", "loss_type": "fire", "amount_usd": 9800, "filed": "2026-09-30"},
        {"claim_id": "C-7701", "loss_type": "storm", "amount_usd": 6100, "filed": "2026-10-04"},
    ],
}

JURISDICTIONS = {"P-1182": "Westmarch", "P-2040": "Eastvale", "P-3301": "Westmarch"}
RULES = {
    (
        "Westmarch",
        "settlement deadline",
    ): "Rule W-14: a covered claim under $10,000 must be settled within 15 days of filing.",
    (
        "Westmarch",
        "water damage",
    ): "Rule W-22: water damage settlements include one inspection, which the desk orders when it settles.",
    ("Eastvale", "settlement deadline"): "Rule E-03: a covered claim must be settled within 30 days of filing.",
    ("Eastvale", "collision"): "Rule E-19: collision settlements under $5,000 need no second appraisal.",
}
DAMAGE = {
    "C-7781": ["kitchen flooring", "lower cabinets"],
    "C-7790": ["rear bumper", "tail light"],
    "C-7802": ["roof shingles"],
}
CATALOGUE = {
    "kitchen flooring": (2100, 14.0),
    "lower cabinets": (1900, 10.0),
    "rear bumper": (640, 3.0),
    "tail light": (185, 0.5),
    "roof shingles": (5200, 22.0),
}
LABOUR_RATE_USD = 90

SMALL_SETTLEMENT_USD = 5000

_ticket_counter = iter(range(90411, 99999))


def next_ticket() -> str:
    return f"CLM-{next(_ticket_counter)}"


def litellm_model() -> dict[str, Any]:
    return {
        "model": os.environ["LLM_SERVICE_GENERAL_MODEL_NAME"],
        "api_key": os.environ["LLM_SERVICE_API_KEY"],
        "base_url": os.environ.get("LLM_SERVICE_ENDPOINT") or None,
    }


def openai_compatible_model() -> dict[str, str]:
    return {
        "base_url": os.environ.get("DEMO_OPENAI_BASE_URL") or os.environ["LLM_SERVICE_ENDPOINT"],
        "api_key": os.environ["LLM_SERVICE_API_KEY"],
        "model": os.environ.get("DEMO_OPENAI_MODEL") or os.environ["LLM_SERVICE_GENERAL_MODEL_NAME"],
    }


def mesh_settings() -> dict[str, Any]:
    """AgentConfig keyword arguments every demo agent shares."""
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    settings: dict[str, Any] = {}
    if endpoint := os.environ.get("DEMO_OTLP_ENDPOINT"):
        from sam_link_sdk.metrics import OtelMetrics, otlp_meter_provider

        # An explicit OTLP/HTTP endpoint is used as given, so it needs the signal path.
        metrics_url = endpoint if endpoint.endswith("/v1/metrics") else endpoint.rstrip("/") + "/v1/metrics"
        otlp_meter_provider(endpoint=metrics_url, export_interval_seconds=10)
        settings["metrics"] = OtelMetrics()
    return settings
