A role is a named bundle of RBAC scopes. The resource header carries
`name` (the role's identity, used wherever the role is referenced) and an
optional `description`. Files live under `rbac/roles/`.

`spec.scopes` lists the scope strings the role grants. Each scope is
`<category>:<resource>:<verb>` — for example `agent_builder:*:update`,
`agent:hr-bot:invoke`, or `rbac:*:read`. `spec.inherits` names other
roles whose scopes this role also grants; inheritance cycles are rejected
at plan time.

`spec.users` grants this role to named identities. Each entry is a bare
identity string (typically an email or subject claim) or a typed subject
`{ type: email|sub, value, issuer? }`. At plan time every entry fans out
into one platform grant, so declaring a user under a role IS the grant.
Which environments a user has the role in is expressed by which manifests
declare that role. To grant a managed/in-DB role that has no config
definition, use a standalone `kind: rbacGrant` file instead.

```yaml
kind: rbacRole
name: sam_builder
description: Author agents, workflows, skills, and toolsets.
spec:
  scopes:
    - agent_builder:*:*
    - workflow_builder:*:*
  users:
    - alice@example.com
    - bob@example.com
```

Grants are managed only when the manifest declares `rbacRoles`. A role
whose `users:` list drops an identity proposes a delete of that grant,
gated behind `--prune` like every other delete. Removing the whole role
from the manifest stops managing its grants (nothing is revoked without
`--prune`).

A grant follows the user, not the spelling of the entry. When a `users:`
entry names a user who already holds the role under another subject (a bare
email where the grant was made with `{ type: email }`, a `{ type: sub }` entry
for that user, or the user's new email once they have signed in with it),
plan shows an update and apply re-records the grant under the entry as
written. The user keeps the role, and `sam config pull` then writes the entry
back the same way. List each user once: when two entries name the same user,
plan keeps one recorded subject and warns about the other. A bare email that
more than one user has names neither of them, and the platform grants it to
whichever of them was updated last, so name such a user by
`{ type: sub, issuer, value }`.

`spec.groups` is rejected. Claim-driven access is authored as a `kind:
rbacClaimMapping` file, which maps an OIDC claim value to a set of roles.
The standalone `rbacAssignment` kind was removed: a manifest that still
declares `resources.rbacAssignments` fails with a message pointing here.

For the scope grammar itself — what goes in each of the three segments,
which verbs are legal, and how to resolve the concrete per-instance scope
for an agent or workflow — read `references/rbac-scopes.md`.
