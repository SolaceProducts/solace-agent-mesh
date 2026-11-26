# Request-driven runtimes (prototype)

Runtimes such as AWS AgentCore Runtime and Azure AI Foundry hosted agents start an agent only when something calls
its endpoint. An agent that waits for its tasks would never be woken by the mesh. Here SAM's `agent_link` app
wakes it instead ([docs/agent-link.md](../../docs/agent-link.md)): it holds the agent's tasks while the agent sleeps,
and when one arrives it calls the agent's `wake` URL. The woken process connects back over the link, serves
what waits and exits.

```
SAM ──► agent_link app ──POST <wake url>──► runtime ──► one agent process per wake
           ▲                                                     │ connects with its token, describes itself,
           └──────────────────── WebSocket ──────────────────────┘ serves what waits (MeshAgent.handle_wake), exits
```

- **`runtime_standin.py`:** a local stand-in for the runtime. It starts a fresh process per POST (`<script> wake`) and
  answers `202` at once, so long tasks don't hold the HTTP call.
- **`coverage_explainer_strands.py`:** a Strands agent written for this. `wake` serves what waits
  (`MeshAgent.handle_wake`); `announce` describes it to SAM once (`MeshAgent.announce`).
- **`echo_app.py`:** the same pattern without a model.
- **`tester_strands.py`:** a tool behind an approval and a question to another agent (policy-desk), from a process
  started per wake.

Locally, with a SAM whose `agent_link` app lists the agent with its token's digest and the stand-in as its wake
(SAM accepts plain `http` only to the local machine):

```yaml
agents:
  - name: coverage-explainer
    token_sha256: <digest from sam-link-sdk link-token>
    wake:
      url: http://localhost:9311/invocations
```

```bash
sam-link-sdk link-token --out coverage-explainer.link-token
export SAM_LINK_URL=ws://localhost:8800/gw/agent-link/v1/connect SAM_LINK_TOKEN=$(cat coverage-explainer.link-token)
python coverage_explainer_strands.py announce
python runtime_standin.py coverage_explainer_strands.py --port 9311
sam task send -a coverage-explainer "Does a silver policy cover seepage?"
```

**What is verified.** Locally, against a real SAM, an agent behind `sam_link_sdk.foundry` and `sam_link_sdk.agentcore` is announced, woken and serves a task, and a task cancelled during a wake ends canceled. It has not been run against the real Azure or AWS runtimes.

**What a request-driven agent pays and needs:**
- **Cold start:** the runtime's own start-up, then one WebSocket connection to SAM.
- **Delivery:** a task stays with SAM until the agent acknowledges it, so a process that crashes mid-task
  gets it again on the next wake (until SAM's task timeout expires it).
- **Announce after deploying,** and again after SAM restarts: SAM keeps an agent that has a
  `wake` listed while it sleeps, until SAM restarts.

## Azure AI Foundry hosted agents

To deploy one, start from the template in `foundry/`. It covers the agent, SAM's `wake` configuration (an
OAuth client signing in to Foundry), the deploy script, and `announce` and `retire`.

- **The wake authenticates as an app registration.** SAM signs in with client credentials (Entra token
  endpoint, scope `https://ai.azure.com/.default`); the app's service principal has Foundry Agent Consumer on the
  project.
- **`handle_wake`** connects, describes the agent, serves every task that waits, and returns once no task is running
  and nothing new has come for 3 s. `sam_link_sdk.foundry` serves before answering the POST: Foundry may suspend the
  sandbox once the response is sent.
- **The handler outlasts the longest human wait.** An approval waits up to 45 minutes; the wake's own time limit
  (`sam_link_sdk.hosted.WAKE_TIMEOUT_SECONDS`, 50 minutes) is longer.

## Amazon Bedrock AgentCore Runtime

`sam_link_sdk.agentcore` applies the same pattern to AgentCore Runtime's HTTP protocol; the template is in
[`agentcore/`](agentcore/README.md).

- **Sign-in.** SAM's wake uses OAuth client credentials against the identity provider the runtime's JWT
  authorizer trusts. With Entra, a token the app requests for itself (`<client id>/.default`) carries the app id as
  `aud` and `appid`; the authorizer accepts that audience and requires `appid` to match (a custom claim), so only
  that app can invoke the runtime.
- **Sessions.** The wake's `headers` carry a fixed `X-Amzn-Bedrock-AgentCore-Runtime-Session-Id`, so wakes land on one
  warm instance until it idles out.
- **Serving outlives the POST.** The wake runs as an AgentCore async task, so the session reports `HealthyBusy`
  during an approval, and it is shielded from the request being dropped when the caller stops waiting.
- **Network.** In VPC mode the runtime leaves through a NAT gateway, so SAM's firewall allows one address.
- **Secrets.** `NAME_SECRET_ID` variables name Secrets Manager secrets read with the execution role at start (for
  example `SAM_LINK_TOKEN_SECRET_ID`).
