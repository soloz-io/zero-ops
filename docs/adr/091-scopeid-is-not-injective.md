# ADR-091: scopeId is not injective

**Date:** 2026-09-28
**Status:** Proposed
**Relates to:** ADR-088 (the axes), ADR-090 (which removed the database role from this construction), ADR-051 (a single spoke serves multiple tenants)

## Context

`scopeId` is `<tenantId>-<appId>`, combined once by the chart so that sixty-five
patch sites need one field rather than a two-field combine. Both components are
constrained to `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, which permits `-` **inside** a
component. The construction is therefore not injective:

```
("foo",     "bar-baz")  →  foo-bar-baz
("foo-bar", "baz")      →  foo-bar-baz
```

No dash-based separator repairs it — `--` is legal inside a component too
(`foo--bar` matches the grammar).

ADR-090 removed the PostgreSQL role from this construction, because a role is a
cluster-wide identity and two tenants sharing a spoke would have been handed one
role owning both their databases. **That reasoning was specific to the role only
because that is what was being fixed.** `scopeId` still names:

- **namespaces** — `tenant-<scopeId>`, so the two pairs above land in one
  `tenant-foo-bar-baz`
- cluster-scoped Crossplane composed resources
- Infisical paths and other cluster-unique names

The namespace is the platform's isolation boundary. Two distinct applications —
belonging to two distinct customers — resolving to one namespace is a
cross-tenant boundary failure, not a naming collision.

## Why this is recorded rather than fixed

Not because it is less severe. It is **more** severe than the role name was: a
namespace carries the workloads, the RBAC, the secrets and the network policy.

It is separate because the fix touches a different and much larger surface. The
role name had one definition after ADR-090 and eight consumers inside one
composition. `scopeId` is, by its own design note, read at roughly sixty-five
patch sites, and the namespace it renders is already live and holding running
workloads — renaming it is not a credential cutover but a workload relocation.
Folding that into ADR-090 would have enlarged a migration that already touches
every provisioned database.

## Options not yet chosen

1. **Constrain the components** so the construction becomes injective — forbid
   `-` inside `tenantId` and `appId`. Cheapest mechanically (a tightened pattern
   plus an audit of existing objects), but it narrows a tenant-facing identifier
   that fleets already use.
2. **Change the delimiter** as ADR-090 did, to a character the grammar forbids.
   `_` is unavailable here: a namespace is an RFC 1123 label. `.` is legal in a
   subdomain but not in a label, and a namespace is a label.
3. **Derive namespaces from an opaque stable identifier** and keep `scopeId` for
   display only. Most robust and most invasive.

## Interim position

No new tenant should be onboarded with a `-` in `tenantId` while this is open, and
an `appId` containing `-` is only safe while its tenant's id contains none. This
is a **convention, not an enforced contract**, which is precisely why it needs
one of the options above rather than a note.

The existing fleets are unaffected: `nutgraf` contains no `-`, so
`nutgraf-waypoint` and `nutgraf-oranger` each have exactly one parse.
