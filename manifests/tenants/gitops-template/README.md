# <TENANT_ID>-gitops

Infrastructure for the **<TENANT_ID>** box. This repository is yours: it lives in
your organisation, your control plane reconciles it, and your cloud account holds
everything it describes.

## Layout

    clusters/<name>/bundle.yaml   the platform version this cluster runs
    clusters/<name>/values.yaml   your configuration for it
    templates/spoke-cluster/      render this to add a cluster

## Who writes what

The platform proposes changes to `bundle.yaml` as pull requests, in the manner of
a dependency-update bot. It never writes `values.yaml`, and it cannot merge its
own proposal.

You write `values.yaml`. It holds only what differs from the bundle's defaults,
so a default can change in a new version without touching anything here, and an
override you have made survives across versions.

The two are separate files so the two writers never conflict. Merging a proposal
is a normal review; accepting them automatically is a repository setting and
changes nothing about what the platform may do.

## Adding a cluster

Copy `templates/spoke-cluster/` to `clusters/<name>/`, replace the tokens, commit.
Your control plane provisions it.

## Leaving

Revoke the platform's app installation. Proposals stop arriving; nothing stops
running. Everything here is already yours.
