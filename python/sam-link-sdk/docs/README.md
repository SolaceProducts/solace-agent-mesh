# Documentation

New here? Start with the [README](../README.md): install, join SAM, run a first agent. Then use this page.

## I want to

| I want to | Read |
|---|---|
| Connect my agent to SAM, or find out why it does not join | [agent-link.md](agent-link.md) |
| Use my framework | [frameworks/README.md](frameworks/README.md), then its page |
| Use a framework with no adapter | [opentelemetry.md](opentelemetry.md) |
| Add tools, and say which users may use them | [frameworks/README.md](frameworks/README.md#the-shared-names), [security.md](security.md#required-scopes-rbac) |
| Ask the user a question or for approval | [handlers.md](handlers.md#asking-the-user) |
| Call another agent, or be a workflow node | [delegation.md](delegation.md) |
| Stream the answer | [frameworks/README.md](frameworks/README.md#streaming-the-answer), [handlers.md](handlers.md#input-and-answer) |
| Keep a conversation across messages | [handlers.md](handlers.md#conversation-memory) |
| Send files with the answer | [handlers.md](handlers.md#files) |
| Limit cost and runaway tasks | [handlers.md](handlers.md#per-task-limits), [behaviour.md](behaviour.md) |
| See tokens and metrics | [behaviour.md](behaviour.md), [opentelemetry.md](opentelemetry.md#the-agents-metrics) |
| Know what is logged and redacted | [security.md](security.md) |
| Understand SAM's stop and what happens on a dropped connection | [operations.md](operations.md) |
| Set an `AgentConfig` field or an environment variable | [configuration.md](configuration.md) |
| Deploy on Azure AI Foundry or Amazon Bedrock AgentCore | [serverless.md](serverless.md) |
| Run a full example | [examples.md](examples.md) |

## Glossary

| Word | Means |
|---|---|
| Agent Mesh, SAM | Solace Agent Mesh, the platform. |
| agent | A program that answers tasks. Yours is a `MeshAgent` built from an `AgentConfig` and a handler. |
| task | One request SAM sends the agent, with its user, text and files. Your handler runs once per task. |
| session | The user plus one chat. `ctx.session_id` names it; memory is keyed by it. |
| peer | Another agent on the mesh that this agent may call. |
| card | What an agent tells SAM about itself: name, description, skills, tools, scopes. |
| scope | A permission string. An agent or a tool can require scopes, and SAM grants them to users. |
| link | The WebSocket connection from your agent to SAM, nothing more. |
| stop | SAM's operator stop: SAM refuses the agent's tasks until it releases it. Not the Stop button in chat (that cancels one task), and not `agent.stop()` (that shuts your process down). |

## How it works

For the curious; nothing here is needed to write an agent.

- [architecture.md](architecture.md): the layers inside the SDK.
- [link-protocol.md](link-protocol.md): the link's protocol, frame by frame.

## Working on the SDK

[CONTRIBUTING.md](../CONTRIBUTING.md) covers setup, tests and the rules for a pull request.
