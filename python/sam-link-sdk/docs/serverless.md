# Request-driven runtimes (Azure AI Foundry, Amazon Bedrock AgentCore)

Runtimes such as Azure AI Foundry hosted agents and AWS AgentCore Runtime start an agent only when something calls its
endpoint. An agent that waits for its tasks would never be woken by the mesh, so SAM wakes it instead: SAM
(see [agent-link.md](agent-link.md)) holds the agent's tasks and calls the runtime when one arrives for an
agent configured with `wake`, and the woken process connects, serves what is waiting and exits. The pattern, a local
stand-in for the runtime and what is verified are in [examples/serverless/README.md](../examples/serverless/README.md);
this page is the map.

## What the SDK gives

| Name | What it does |
|---|---|
| `MeshAgent.handle_wake()` | A wake: connects, describes the agent, learns its peers and SAM's stop, takes every task waiting for it, and returns once no task is running and nothing has been delivered for 3 s. |
| `MeshAgent.announce()` / `MeshAgent.retire()` | Describe the agent once / take it off the mesh: `announce` after deploying and after SAM restarts, `retire` when the deployment is deleted. |
| `sam_link_sdk.foundry.serve(build_agent)` / `.app(build_agent)` | The Foundry hosted-agent entry point around `handle_wake` (extra `foundry`). |
| `sam_link_sdk.agentcore.serve(build_agent)` / `.app(build_agent)` | The AgentCore Runtime entry point around `handle_wake` (extra `agentcore`); `load_secrets` reads `NAME_SECRET_ID` variables from Secrets Manager (`SAM_LINK_TOKEN_SECRET_ID`). |
| `SAM_LINK_URL` | SAM's connect URL a woken agent uses; `serve` passes it to `build_agent(link_url)`, which builds the agent for it. |
| `sam-link-sdk link-token` | Issues the agent's link token and prints the digest SAM's configuration takes. |

```bash
pip install "sam-link-sdk[foundry]"      # azure-ai-agentserver-invocations
pip install "sam-link-sdk[agentcore]"    # bedrock-agentcore, boto3
```

## Templates

- [examples/serverless/foundry/](../examples/serverless/foundry/README.md): an agent, SAM's `wake`
  configuration, the deploy script, and `announce` and `retire`.
- [examples/serverless/agentcore/](../examples/serverless/agentcore/README.md): the same for AgentCore Runtime.
- `examples/serverless/runtime_standin.py`, `coverage_explainer_strands.py`, `echo_app.py` and `tester_strands.py`:
  the pattern locally, with a stand-in for the runtime.

Both templates join through the link (`SAM_LINK_URL`, `SAM_LINK_TOKEN`); `requirements.txt` is all a
deployment installs.

## What a request-driven agent pays and needs

- **Cold start:** the runtime's own start-up, then one connection to SAM.
- **Delivery:** a task stays with SAM until the agent acknowledges it, so a process that crashes mid-task
  gets it again on the next wake, until SAM's task timeout expires it.
- **Listing:** SAM lists the agent, marked on-demand, from its last description while it sleeps. SAM keeps
  that listing in memory, so after SAM restarts a request-driven agent is not listed until it announces
  again (`python main.py announce`) or a wake describes it. Run `announce` after every SAM restart.
- **A stopped request-driven agent stays known to SAM:** SAM refuses its tasks while it is stopped and does not wake
  it, and a release lets it be woken again. A wake learns SAM's stop when it connects; a request-driven agent saves no
  stop of its own.

The shipped wrappers build a fresh `MeshAgent` for every wake. A `MeshAgent` reused across wakes also works: a wake the
runtime cuts short abandons and forgets its running tasks (SAM delivers them again, and the next wake runs them), and
`on_stop_change` listeners hear a release when a later wake finds the agent released after an earlier one found it
stopped.
