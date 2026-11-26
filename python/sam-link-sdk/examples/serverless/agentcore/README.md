# An Agent Mesh agent on Amazon Bedrock AgentCore (template)

A model-driven agent, built with Strands and the `sam_link_sdk` SDK, deployed to Amazon Bedrock AgentCore Runtime. Nothing
of it runs between requests. SAM wakes it for each request, and governs it like one of its own agents:
- it's listed in SAM;
- each user is verified;
- its model and tool steps are recorded, and every model call's tokens go to SAM's Cost and Token Usage view;
- it asks before `pay_claim` runs;
- it can call other agents on the mesh, and they can call it;
- it can be stopped from the console.

It joins through SAM's `agent_link` gateway entrypoint ([docs/agent-link.md](../../../docs/agent-link.md)): it reaches
only the gateway, over one WebSocket, with a token of its own. The entrypoint verifies each user and enforces the
console's stop, holds the agent's tasks while it sleeps, and wakes it:

```
SAM ──► agent_link entrypoint ──POST (OAuth JWT)──► AgentCore /invocations ──► session ──WebSocket──► entrypoint
                                                                               sam_link_sdk.agentcore
```

It's the same pattern as the [Foundry template](../foundry/README.md); only the wrapper (`sam_link_sdk.agentcore`), the
deploy script and the wake's sign-in differ.

| File | What it is |
|---|---|
| `agent.py` | The agent: tools, prompt, card. Replace with your own; `build_agent(link_url)` is the only contract. |
| `main.py` | AgentCore's entry point (`sam_link_sdk.agentcore.serve`), also `announce` and `retire`. |
| `deploy.py` | Packages this folder with the SDK for Linux arm64, uploads it to S3, creates or updates the runtime. |
| `requirements.txt` | Packaged into the zip by `deploy.py`: the agent's dependencies. |

## Before you start

- **SAM's agent link entrypoint** reachable from the runtime (`wss://<gateway>/gw/<gateway_id>/v1/connect`), on a SAM
  that speaks agent link protocol 2. AgentCore's VPC mode is the controlled way: the runtime's traffic leaves through
  your NAT gateway.
- **An OAuth client the entrypoint signs in as** to wake the runtime, in an identity provider the runtime's JWT
  authorizer trusts: a Cognito app client with the client-credentials grant, or an Entra app registration, or Okta.
- **An execution role** for the runtime: AgentCore's runtime permissions, `s3:GetObject` on the code package,
  `secretsmanager:GetSecretValue` on the agent's secrets, and `bedrock:InvokeModel` for a Bedrock model.
- **A model**: a Bedrock model or inference profile (`BEDROCK_MODEL_ID`), or any OpenAI-compatible endpoint
  (`AZURE_OPENAI_*`).

## Deploy

1. **Issue the agent's token:** `sam-link-sdk link-token --out claims-assistant.link-token`. It prints the token's SHA-256
   digest; the token stays in the file. Store it in Secrets Manager.
2. **Write `agent.env`**, naming the secrets rather than holding them:

   ```
   SAM_LINK_URL=wss://<gateway>/gw/<gateway_id>/v1/connect
   SAM_LINK_TOKEN_SECRET_ID=<the secret holding claims-assistant.link-token>
   BEDROCK_MODEL_ID=<model or inference profile>
   ```

   Set `SAM_LINK_TRUST_STORE_PATH` only for a gateway with a private certificate, and `SAM_PAY_CLAIM_SCOPE` (for
   example `tool:claims:pay`) for SAM to run `pay_claim` only for users who hold that scope.

3. **Deploy the runtime** (with AWS credentials that can create AgentCore runtimes and pass the role):

   ```bash
   pip install -e '.[agentcore,strands]'         # in python/sam-link-sdk; deploy.py also needs uv
   python deploy.py --agent claims_assistant --role-arn <execution role ARN> --bucket <code bucket> \
     --env-file agent.env --discovery-url <IdP>/.well-known/openid-configuration --audience <token audience> \
     --subnets <private subnet ids> --security-groups <egress-only security group>
   ```

   With Entra as the identity provider, a token requested for the app itself carries the app id as its audience and
   as `appid`, so `--audience <client id> --claim appid=<client id>` admits only that app, and the discovery URL is
   `https://login.microsoftonline.com/<tenant>/.well-known/openid-configuration`.

4. **Configure the agent on the entrypoint** (SAM's gateway configuration), with the digest and the wake call, using
   the runtime ARN `deploy.py` printed:

   ```yaml
   agents:
     - name: claims-assistant
       token_sha256: <digest>
       peers: [policy-desk]
       wake:
         url: https://bedrock-agentcore.<region>.amazonaws.com/runtimes/<url-encoded runtime ARN>/invocations?qualifier=DEFAULT
         token_url: <your identity provider's token endpoint>
         client_id: <OAuth client the runtime's JWT authorizer trusts>
         client_secret: ${WAKE_CLIENT_SECRET}
         scope: <its scope>
         headers:
           X-Amzn-Bedrock-AgentCore-Runtime-Session-Id: <33+ character session id>
   ```

5. **List it in SAM:** `python main.py announce`, with the same environment and the token, after deploying and again
   after SAM's gateway restarts. The entrypoint wakes the agent, so it keeps listing it while it's asleep.

Chat with **Claims Assistant** in SAM. A stop from the console cancels the agent's running tasks, and SAM refuses its
new ones until released.

## Retire

```bash
python main.py retire                          # SAM stops listing it
```

Then remove the agent from the entrypoint's configuration and delete the runtime
(`aws bedrock-agentcore-control delete-agent-runtime`).

## Good to know

- **Sessions.** Every wake carries the session id set in the entrypoint's `wake.headers`, so wakes reuse one warm
  instance while it lives (`--idle-seconds`, 10 minutes by default).
- **Approvals hold a session.** The serving runs as an AgentCore async task, so the session reports `HealthyBusy`
  while the agent waits for the person, up to the approval's 45-minute timeout.
- **Secrets.** The wake's OAuth secret lives with the entrypoint; the agent's link token and any model key live in
  Secrets Manager, read with the execution role at start.
- **Crashes.** A session that crashes mid-task leaves the task unacknowledged, so the entrypoint delivers it again on
  the next wake, and SAM's task timeout expires it after that.
