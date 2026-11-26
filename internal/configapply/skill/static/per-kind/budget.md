A budget sets a limit on LLM spend for each budget period, a calendar month in
UTC. Each budget has a budget scope. The **Agent Mesh budget**
(`subjectType: mesh`) limits the total spend of all of Agent Mesh; there is
exactly one. A **user budget** (`subjectType: user`) limits each user assigned
to it separately (their spend is never combined), and the default user budget
applies to every user with no assignment. Agent Mesh starts with both, unlimited: an Agent
Mesh budget named `Agent Mesh` and a default user budget named `Default User`.
Files live under `budgets/`.

```yaml
kind: budget
name: Agent Mesh
spec:
  subjectType: mesh
  amount: "5000"
  warningPercentage: 80
```

```yaml
kind: budget
name: Sales
spec:
  subjectType: user
  amount: "250"
  warningPercentage: 90
  users:
    - alice@example.com
    - system:eval
    - { type: sub, value: "00u1abcd", issuer: "https://idp.example.com" }
```

**The file states the whole budget.** `amount` is required: write
`amount: null` for an unlimited budget. Omitting `warningPercentage` clears
the threshold. Because omission clears, an unknown or misspelt spec key fails
the plan rather than being ignored. `amount` is in US dollars with at most two
decimal places; `19.9`, `"19.9"` and `19.90` are the same amount. Quote an
amount with many digits, since a YAML decimal number with more than 15 significant
digits is refused. `warningPercentage`, the UI's **Alert Threshold**, is 1 to
100 and needs an amount.

**Matching and renames.** The Agent Mesh budget is matched by budget scope,
not by name, so declaring it under a new name renames it in place. A user budget is matched by
name, ignoring case: a change of casing renames it in place, and any other new
name plans as a delete of the old budget plus a create, with the delete gated
behind `--prune`. An Agent Mesh budget that no file declares is left alone and
is never pruned.

**The default user budget.** `isDefault: true` makes a user budget the default.
Omitting `isDefault` leaves the default to whoever manages it in the UI,
because a default can only be moved to another budget, never cleared:
`isDefault: false` on the current default fails the plan unless another budget
is marked `isDefault: true`, and two budgets marked `true` fail it too. On
the Agent Mesh budget, `isDefault: false` fails the plan. Prune
never removes the budget that will still be the default once the apply's
promotions land; the plan keeps it with a warning.

**Users.** `spec.users` lists a user budget's assignees, and is not allowed on
the Agent Mesh budget. Each entry is one of:

- An email, as a bare string.
- A service account, as `system:<name>`. The built-in `default`, `channel`,
  `eval` and `evidence` are allowed (the evidence service books its model
  spend to `evidence`), as is a service account that already exists or one created
  in the same apply.
- A typed identity, `{ type: email|sub|system, value, issuer? }`. `issuer` is
  required for a `sub` and allowed only for one.

A file with the key manages the budget's whole assignee list. An entry moves
its user to this budget from any other budget. Removing an entry deletes that
assignment, which returns the user to the default budget and needs `--prune`.
A file without the key leaves the budget's assignments to the UI, while
`users: null` or `users: []` removes every assignee and needs `--prune`. An entry for
someone who has not signed in yet applies at their first sign-in. `system:channel` is the
exception to "the whole list": the platform hides it from its listings, so a
file naming it matches its assignment, but the plan never removes that
assignment and pull never writes it.

Name each user by the identity they sign in with now. An entry that finds a
signed-in user's assignment by an identity other than the recorded one (a new
email, or a `sub` for a user an email assigned) plans an update that re-records
the identity for the same user (`identity: <old> → <new> (same user)`); apply
records it, with or without `--prune`, and pull then writes it.
The same user on two budgets, two entries naming the same user, and an unknown
service account all fail the plan.

**Deleting a budget.** Deleting a budget removes its assignments, so its
users revert to the default user budget.
Prune deletes such a budget with a warning naming how many users revert, less
any the same apply moves to a budget the files declare: assignments apply
before budget deletes, so renaming a user budget whose file carries its
`users` moves them rather than reverting them. If a planned move off a budget
fails (its target's create is refused, say), apply keeps the budget and
reports it `skipped (assignees_not_moved)`.

**Not managed here:** additional temporary amounts. An additional amount for
the current period is set in the Agent Mesh UI, and declarative config leaves
it unchanged.

**Emitted by `sam config pull`,** so an existing deployment round-trips: pull
writes every budget, the Agent Mesh budget and `Default User` included. `amount` is
always written, `isDefault: true` only on the default user budget, and `users`
on every budget with assignees. A budget whose name
cannot be a file name, such as `Sales/EMEA`, gets a derived file name; apply
matches on the `name` in the file, not the file name. Re-applying the pulled
files changes nothing.

**Token scopes.** Each API call apply makes is checked on its own, so the
token needs only what the files change: `budget:*:read` always;
`budget:*:create`, `budget:*:update` or `budget:*:delete` for the budgets it
creates, changes or deletes; `budget:*:assign` when it changes the default user
budget, assigns, moves, removes or re-records users, or deletes a budget; and
`rbac:*:read`
when a file declares `users`. A pull without `rbac:*:read` writes budgets without their
users and says so, and a full pull without `budget:*:read` skips budgets and
says so. The budget write scopes are for super administrators only, so apply
budget changes with a super administrator's token; do not put the write scopes
on a role, because nothing stops their holder from raising their own limit.
