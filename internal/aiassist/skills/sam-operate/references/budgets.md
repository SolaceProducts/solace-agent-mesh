# Model spend: pricing, budgets, assignments, usage

Agent Mesh records the cost of every LLM call and lets an operator set monthly budgets on that cost, which it enforces as hard limits. The full operator guide is the `sam-docs` page `references/documentation/administering/budgets/index.md`; budgets as YAML are the `budget` kind in `sam-declarative-config`. This file is the short, verified shape so you route and answer correctly.

## Spend is only as good as the prices

- A call is priced from the rates on the **model configuration** it used, in USD per 1,000,000 tokens: input, output, and optional cache-read and cache-write rates (cached tokens fall back to the input rate; a decision model has input and output only). Set them on the **Models** page (the pricing step when adding or editing a model), or with the `pricing` field of the `model` kind (`sam-declarative-config`).
- **Unpriced is not free.** A model with no price records tokens but no cost, so its calls count toward no budget and show as $0; the Usage page counts them separately as calls with no price, so treat them as missing data, not as free. To record a model as costing nothing, set its input and output rates to `0`. "Spend shows $0" for a model in use almost always means it is unpriced.
- A call is priced when it is recorded. A price change affects later calls only; recorded spend is never re-priced. Spend records are kept for 90 days.

## Budgets

- Two budget scopes, both enforced at once. The **Agent Mesh** budget limits the total spend of the deployment; there is exactly one, and it cannot be deleted. A **user** budget limits **each** user assigned to it separately: every user on it can spend up to its limit, and their spend is never combined, so it is not a team cap. One user budget is the **default**, which applies to every user with no assignment. A fresh deployment has `Agent Mesh` and `Default User`, both unlimited.
- A user with no assignment **follows the default** and moves when the default changes; an assigned user is **pinned**, even to the current default. Removing the assignment returns them to following the default.
- One period for every budget: the calendar month in UTC. It cannot be changed.
- **Limit (USD)**, at most two decimal places, or **Unlimited**. **Alert Threshold** is an optional whole number 1–100, the percentage of the limit at which the budget is approaching it; reaching it refuses nothing. An unlimited budget cannot have one.
- **Budget alerts:** at the alert threshold and again at the limit, Agent Mesh sends a notification (notifications are Early Access). Each user is notified about their own budget; holders of `budget:*:update` or `budget:*:assign` are notified about the Agent Mesh budget and service accounts' budgets.
- Manage budgets under **System Settings › Budgets** (visible with `budget:*:read`). Change one user's budget from the **User Management** page (**Budget** column, **Manage Budget** in the user's menu); that page is visible only to a super administrator.
- Deleting a budget needs `budget:*:delete` and `budget:*:assign`; its assignees revert to the default user budget. The current default cannot be deleted; change the default first.
- **Desktop bundle:** no user budgets in the UI. Its single user stays on the unlimited default user budget, so the Agent Mesh budget is the only limit that applies.
- To assign someone who has not signed in yet, use `sam config apply` (a `budget` file's `users`); the assignment applies at their first sign-in.

## Additional temporary amount

Raises a user's limit until the end of the current UTC month without changing the budget. Greater than 0 and at most $100,000.00; a new amount replaces the current one rather than adding to it; not available on an unlimited budget. Set it from **Manage Budget**. Declarative config never manages additional amounts.

## Cost and token usage

- **Signals & Usage › Usage** opens the **Cost and Token Usage** page (needs `*:*:read_usage`): total spend for a period within the last 90 days, compared with the previous period; spend by **Agents**, **Workflows**, **Users** and **Models**; a banner counting calls to unpriced models.
- Every user sees their own spend, with no scope, under **User Account › Usage**: this month against their budget, and the last three months.

## Scopes

`budget:*:read` (view budgets, the default, and the budget and additional amount that apply to each user who has signed in and to each service account, with their names and email addresses — treat it as a read of people, not just of numbers), `budget:*:create`, `budget:*:update`, `budget:*:delete`, and `budget:*:assign` (change the default, assign users, grant or cancel additional temporary amounts; assigning and additional amounts also need `budget:*:read`). `*:*:read_usage` opens every user's spend and token usage; reading your own needs no scope. Of the built-in roles, only `sam_manager` grants these: it holds `*:*:read_usage` and `budget:*:*`, every budget scope. The UI role editor offers none of them. `budget:*:read` and `*:*:read_usage` can be added to a role through declarative config; only super administrators and `sam_manager` hold the four write scopes. Do not suggest putting the write scopes on a custom role: nothing stops a holder of `assign` or `update` from raising their own limit, and that holds for `sam_manager` holders too ([rbac.md](rbac.md)).

## When a budget is spent

A budget is spent when the period's spend reaches its limit plus any additional temporary amount; for a user budget, that is the user's own spend. An unlimited budget never is, and a limit of `0` is spent from the start (a `0` default user budget refuses every user with no assignment). A spent Agent Mesh budget refuses everyone; a spent user budget refuses that user, and work that runs as a service account is held to that service account's budget.

- **Refused:** new requests at every entrypoint; the next LLM call of a running request (the call in progress finishes, then the request stops); LLM calls tools make through Agent Mesh (tools that call no model still run); the platform's own LLM calls, such as the AI assistant, the prompt and task builders, evaluations and model connection tests.
- **The message** names whose budget is spent and when it resets ("…usage budget is used up until <date> UTC…"), never the amount or the budget's name.
- **Ending a refusal:** raising the limit, making it unlimited, granting an additional temporary amount, or moving the user usually takes effect within a few seconds; every budget resets at 00:00 UTC on the 1st.
- **"Usage budgets can't be checked at the moment":** not a spent budget. An Entrypoint Executor that restarted, or the users of a budget that was spent at a month rollover, are refused until the Platform service sends the current state; it is transient. If it persists, check the Platform service is up.

Details, including each message, are on the docs page above (**What Happens When a Budget Is Spent**).
