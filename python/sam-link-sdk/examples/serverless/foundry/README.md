# An Agent Mesh agent on Azure AI Foundry (template)

A model-driven agent, built with Strands and the `sam_link_sdk` SDK, deployed as an Azure AI Foundry hosted agent. Nothing
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
SAM ──► agent_link entrypoint ──POST (OAuth)──► Foundry /invocations ──► sandbox ──WebSocket──► entrypoint
                                                                         sam_link_sdk.foundry
```

| File | What it is |
|---|---|
| `agent.py` | The agent: tools, prompt, card. Replace with your own; `build_agent(link_url)` is the only contract. |
| `main.py` | Foundry's entry point (`sam_link_sdk.foundry.serve`), also `announce` and `retire`. |
| `deploy.py` | Packages this folder with the SDK and deploys it as a hosted agent. |
| `requirements.txt` | Installed by Foundry's remote build: the agent's dependencies. |

## Before you start

- **SAM's agent link entrypoint** reachable from Azure (`wss://<gateway>/gw/<gateway_id>/v1/connect`), on a SAM that
  speaks agent link protocol 2.
- **From an Azure admin, once.** An app registration with a client secret, its service principal holding
  **Foundry Agent Consumer** on the project. The entrypoint signs in as it to wake the agent.
- **A model deployment** in the Foundry account, for example `gpt-4o-mini` on the Global Standard (pay-per-token) SKU.

## Deploy

1. **Issue the agent's token:** `sam-link-sdk link-token --out claims-assistant.link-token`. It prints the token's SHA-256
   digest; the token stays in the file.
2. **Configure the agent on the entrypoint** (SAM's gateway configuration), with the digest and the wake call:

   ```yaml
   agents:
     - name: claims-assistant
       token_sha256: <digest>
       peers: [policy-desk]
       wake:
         url: https://<account>.services.ai.azure.com/api/projects/<project>/agents/claims-assistant/endpoint/protocols/invocations?api-version=v1
         token_url: https://login.microsoftonline.com/<tenant>/oauth2/v2.0/token
         client_id: <app registration client id>
         client_secret: ${WAKE_CLIENT_SECRET}
         scope: https://ai.azure.com/.default
   ```

3. **Write `agent.env`** (kept out of the package and out of source control):

   ```
   SAM_LINK_URL=wss://<gateway>/gw/<gateway_id>/v1/connect
   SAM_LINK_TOKEN=<from claims-assistant.link-token>
   AZURE_OPENAI_ENDPOINT=https://<account>.openai.azure.com
   AZURE_OPENAI_API_KEY=<account key>
   AZURE_OPENAI_DEPLOYMENT=gpt-4o-mini
   SAM_SPEND_MODEL_KEY=<optional: a SAM model alias carrying this model's prices, so its calls are costed>
   SAM_PAY_CLAIM_SCOPE=<optional: a scope pay_claim requires, for example tool:claims:pay>
   ```

   Set `SAM_LINK_TRUST_STORE_PATH` only for a gateway with a private certificate.

4. **Deploy** (signed in with `az login`, with a role that can create agents on the project):

   ```bash
   pip install -e '.[foundry,strands]'            # in python/sam-link-sdk
   pip install azure-ai-projects azure-identity
   python deploy.py --project-endpoint https://<account>.services.ai.azure.com/api/projects/<project> \
     --agent claims-assistant --env-file agent.env
   ```

5. **List it in SAM:** `set -a; . ./agent.env; set +a; python main.py announce`, after deploying and again after SAM's
   gateway restarts. The entrypoint wakes the agent, so it keeps listing it while it's asleep.

Chat with **Claims Assistant** in SAM. The first request after a quiet period takes about 10 s while Foundry starts a
sandbox. A stop from the console cancels the agent's running tasks, and SAM refuses its new ones until released.

## Retire

```bash
python main.py retire                          # SAM stops listing it
```

Then remove the agent from the entrypoint's configuration and delete the hosted agent in Foundry.

## Good to know

- **Cold start.** Every request that finds no sandbox awake starts one, which takes about 10 s. A sandbox still
  awake (3 s after its last message, or while it waits for an approval) serves new requests at once.
- **Approvals hold a sandbox.** It stays running while it waits for the person, up to the approval's 45-minute
  timeout.
- **Secrets.** The app registration's secret expires; rotate it where the entrypoint's configuration keeps it. The link
  token and the model key are environment variables of the deployment. For production, prefer Key Vault references or
  the hosted agent's managed identity for the model.
- **Crashes.** A sandbox that crashes mid-task leaves the task unacknowledged, so the entrypoint delivers it again on
  the next wake, and SAM's task timeout expires it after that.
