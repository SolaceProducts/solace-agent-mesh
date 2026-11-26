A service account is a non-human principal: the identity an entrypoint
runs as, or the identity a pipeline uses to call the platform API. The
platform derives its subject as `system:<name>`. Files live under
`rbac/service-accounts/`. Until 2027-12-01 the kind is also accepted as
`systemUser`, under `resources.systemUsers` and `rbac/system-users/`, the
spelling used before the rename; `sam config plan` warns once per run when a
repository still uses it. To move a service account over, change its
directory, its `kind:` and its manifest key together, in every manifest that
lists it; `plan` refuses a service account that mixes the two spellings.
Different service accounts can use different spellings, but every `sam` that
plans or applies the repository, CI images included, must be on a release with
the rename before any service account moves to `serviceAccounts`. An older
`sam` ignores that key: with only `serviceAccounts` it plans nothing for
service accounts, and alongside `systemUsers` it plans a delete for each
service account under `serviceAccounts`, which `apply --prune` carries out,
removing that account's grants, API keys and budget assignments with it.
`sam config plan` warns when a manifest lists both keys. `sam config pull`
keeps whichever layout a repository already has, and warns when both
directories exist.

`spec.name` is the service account's name and defaults to the resource header's
`name` when omitted. `spec.displayName` is the friendly label shown in the
Access Control UI. `spec.roleNames` is required and lists the roles granted,
by name — each resolves to a role id at plan time, or at apply time for a role
created earlier in the same apply. A named role may be config-defined or
managed/in-DB.

```yaml
kind: serviceAccount
name: ci-deployer
spec:
  displayName: CI Deployer
  roleNames:
    - sam_builder
```

**The file is the source of truth.** `apply` makes the platform match it, so
editing `roleNames` changes what the service account holds in both directions:

- **Adding a role grants it.** Applied on an ordinary `apply`.
- **Removing a role revokes it.** Also applied on an ordinary `apply` — no flag.
  The plan names the roles it will take away, so read that line before applying:
  it is the one place an apply reduces what an entrypoint can reach. Anything the
  principal picked up outside config is revoked too, since the file is the whole
  role set rather than an addition to it.
- **Removing the resource deletes the principal**, gated behind `--prune` like
  every other kind. Dropping the file without `--prune` leaves it in place and
  plans the deletion for the next pruning apply.

`spec.name` cannot be changed in place: the subject `system:<name>` *is* the
principal's identity, so a new name is a new principal. Renaming plans as a
delete plus a create, and the delete needs `--prune`. Point the entrypoint's
`run_as` at the new name in the same change.

**Emitted by `sam config pull`,** so an existing deployment round-trips: pull
writes a file for every service account config owns, and re-applying it changes
nothing. The built-in principals are never written, because they cannot be
authored.

**The built-in service accounts cannot be authored.** Agent Mesh builds in
`default`, `channel`, `eval`, and `evidence`, and `plan` rejects a file naming
any of them (in the bare or the `system:`-prefixed spelling). Provisioning one would not do what it
looks like it does: the built-in roles resolve from the embedded defaults and
are unioned with anything you grant, so the file could only widen the
principal's reach, never narrow it. To give an entrypoint less than the default
service account holds, declare a distinct service account with only the scopes it needs
and point that entrypoint's `run_as` at it. The same applies to `eval`, which
evaluation experiments run as when their `runAs` is unset: scope an experiment
down by declaring your own service account and naming it in `runAs`, never by
trying to author `eval`.
