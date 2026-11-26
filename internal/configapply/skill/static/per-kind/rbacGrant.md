A grant file grants an existing role to a set of identities. Use it for a
managed/in-DB role — one shipped by the platform, assignable but not
editable — that has no config definition to carry `spec.users`. The
resource header carries `name` (a local handle) and files live under
`rbac/grants/`.

`spec.roleName` names the role to grant, referenced by name; it may be a
config-defined role or a managed/in-DB role that exists only on the
platform. `spec.users` lists the identities in the same shape as
`spec.users` on a role: each entry is a bare identity string (legacy,
email-preferred) or a typed subject `{ type: email|sub, value, issuer? }`.
Each entry fans out into one platform grant at apply time.

```yaml
kind: rbacGrant
name: platform-admins
spec:
  roleName: sam_manager          # a managed role, not defined in config
  users:
    - ops@example.com
    - type: sub
      value: u-8f21
      issuer: https://idp.example.com
```

Prefer `spec.users` on the role for a config-defined role — a grant file
that targets one still applies but draws a plan-time advisory. Grants are
managed when the manifest declares `rbacRoles` or `rbacGrants`; dropping a
subject proposes a delete of that grant, gated behind `--prune` like every
other delete. As with `spec.users` on a role, an entry that names a user who
already holds the role under another subject re-records that grant in place
(plan shows an update) rather than revoking it.
