# Changelog

All notable changes to `sam-link-sdk`, the Solace Agent Mesh Link SDK, are listed here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). While it is in `0.x`, a breaking change bumps the minor
version (`0.1 -> 0.2`), not the patch. A release renames the Unreleased section to `## [X.Y.Z] - YYYY-MM-DD`.

## Unreleased

### Changed

- The `LinkAuthRefused` message also covers a link agent registered on the platform: the agent's name must be the link
  agent's `instanceName`, and the link agent must be deployed.
- The `adk` extra requires LiteLLM 1.104 or later (it allowed 1.60). With the lowest supported Google ADK (2.14.1), a
  LiteLLM before 1.84, ADK's own lower bound, fails every model response with a tool call, and one before 1.104 counts
  tokens itself when the model reports none, so the task recorded usage the model never reported. No adapter change can
  tell those counts from reported ones.

### Added

- The examples read `CLAIMS_PRICING_KEY`, the alias of the SAM model configuration whose prices cost the model's calls
  (`AgentConfig.spend_model_keys`). The examples docs say that SAM's `.env` model name carries a provider prefix only
  the ADK and CrewAI examples take.
- The OpenTelemetry adapter, and the Azure AI Foundry and Amazon Bedrock AgentCore runtimes when they build their
  server, raise `FrameworkDrift` when the installed release lacks a hook they use, as the other adapters do.
- The framework table says the lowest supported release of each framework, which CI tests with the framework
  installed alone ([framework-releases.md](docs/contributing/framework-releases.md)).

### Fixed

- With CrewAI 1.15.27 or 1.15.28, a task whose conversation outgrew the model's context window failed without saying
  so: those releases drop the provider's error once their summarizing gives up. It now fails as a context overflow of
  the crew's model provider, as it does on later releases.
- The CrewAI example passes `context_window_size` only to a CrewAI release that has it (1.15.34 and later). Older
  releases sent it to the provider, which refused every model call.

## [0.1.0] - 2026-11-25

The first public release, in Early Access: the API can change in any release, and this file lists each change.

- Join a running Solace Agent Mesh as a first-class agent over one WebSocket, the link: `MeshAgent`, `AgentConfig`,
  `TaskContext`.
- Adapters for LangChain, LangGraph, the OpenAI Agents SDK, Google ADK, CrewAI, Strands Agents, Microsoft Agent Framework
  and Hermes Agent, and an adapter for any framework that emits OpenTelemetry GenAI spans. Each reports model and tool
  steps, token usage and the audit trail, enforces per-tool scopes and tool approvals, and streams the answer.
- Request-driven runtimes: Azure AI Foundry hosted agents and Amazon Bedrock AgentCore.
- The `sam-link-sdk link-token` command, which issues an agent's token and prints the digest SAM's configuration takes.
- An agent whose `AgentConfig.framework` is empty is described as built with the framework of the one sam_link_sdk
  adapter its process imported (`LangChain`, `CrewAI`, `Google ADK`, `OpenAI Agents SDK`, `Strands Agents`,
  `Microsoft Agent Framework` or `Hermes Agent`), so SAM shows what an agent built with a single adapter is built with.
  A framework the author names still wins; with no adapter, or adapters for more than one framework, it stays unnamed.
- `start()` fails with `DescriptionTooLong` (from `sam_link_sdk.link`) when the agent's description is over 4096 bytes,
  it has more than 32 skills, or a skill's name is over 256 bytes or its description over 1024, held to the limits SAM
  announces. SAM refuses such a description and gives the agent no task.
- A connection answered with HTTP 503 is tried again after the answer's `Retry-After`, so an agent behind a plain load
  balancer reaches the replica of a multi-replica SAM that serves agent links. A connect that gives up raises
  `LinkUnavailable` (a `LinkError` from `sam_link_sdk.link`, carrying `retry_after`).
- `run_forever()` also serves on a loop that cannot handle signals (on Windows, or off the main thread).
