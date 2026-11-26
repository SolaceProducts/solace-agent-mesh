A claim mapping grants a set of roles to every identity whose OIDC token
carries a matching claim value, so access follows the IdP's group
membership instead of a per-user grant. The resource header carries `name`
(a local handle) and files live under `rbac/claim-mappings/`.

The OIDC claim key to read is a single deployment-wide setting
(`external_auth_claim_key`) — it is NOT set per mapping and cannot be
authored here.
`spec.oidcProvider` names the configured provider; `spec.claimValue` is the
value of that global claim key that grants the roles (for example the
`groups` claim carrying `sam-admins`); `spec.roleNames` lists the roles to
grant, each referenced by name. Every referenced role must be a DB-managed
`rbacRole` (declared in the manifest or already on the platform), not an
operator-owned YAML role. `spec.name` is an optional label (defaults to the
resource header).

`spec.oidcProvider` must be the provider name that appears in sign-in tokens —
the key of the `providers:` catalog entry the deployment authenticates against.
`enterprise` and `generic` are normally internal placeholders rather than
provider names, so apply rejects a mapping using either one with a 422 **when it
is not the deployment's configured provider**; a mapping stored under it could
never match a token. The same check runs on an update that repoints a mapping,
since `oidcProvider` and `claimValue` together identify it.

A deployment whose IdP is genuinely named `enterprise` is accepted, as is any
value when the platform has no configured provider to compare against — there
is nothing to check it against. That last case means the check only protects
deployments that set `authorization_service.idp_claims_config.oidc_provider`.

The value is refused rather than corrected because a mapping is identified by
`oidcProvider` plus `claimValue`, so rewriting one would make every later plan
show a change.

```yaml
kind: rbacClaimMapping
name: azure-admins
spec:
  oidcProvider: azure
  claimValue: sam-admins
  roleNames:
    - sam_manager      # built-in platform role
    - sam_builder      # a role declared in rbac/roles/
```

Claim mappings and per-user grants are independent: use `spec.users` on
the role for named individuals, and a claim mapping for group-driven
access. Both can grant the same role.
