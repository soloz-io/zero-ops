# <TENANT_ID>-gitops

Infrastructure for the **<TENANT_ID>** box. This repository is yours: it lives in
your organisation, your control plane reconciles it, and your cloud account holds
everything it describes.

## Layout

    clusters/<name>/bundle.yaml              the platform version this cluster runs
    clusters/<name>/values.yaml              your configuration for it
    environments/<env>/<fleet>/values.yaml   what this fleet is: cell, quotas, hostnames
    environments/<env>/<fleet>/workloads/    the versions of your applications it runs
    templates/spoke-cluster/                 render this to add a cluster
    templates/fleet/                         render this to add a fleet
    templates/workload/                      render this to run an application

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

## Running an application

A fleet first: copy `templates/fleet/values.yaml` to
`environments/<env>/<fleet>/values.yaml` and fill in the fleet's id, its cell, and its
quota. That file existing is what makes the fleet real -- your control plane globs
for it -- and it gets the fleet a namespace, a quota and its hostnames.

Then copy `templates/workload/` to `environments/<env>/<fleet>/workloads/` and add one
dependency per application.

Your application's own repository holds its source and its Helm chart, builds it,
publishes that chart to a registry, and then commits the new version into the
`Chart.yaml` here. That commit is the deployment: your control plane reconciles
it on the next pass.

What lives here is a version and your values -- never an image digest, an overlay
or a rendered manifest. Two consequences are worth knowing. What you are running
is legible from this file without resolving anything, and a rollback is an edit to
one line, because the version you are going back to still exists in the registry.

Your build writes `Chart.yaml`; you write `values.yaml`. Same division as
`bundle.yaml` and `values.yaml` above, and for the same reason: two writers, no
shared field, nothing to arbitrate.

## Leaving

Revoke the platform's app installation. Proposals stop arriving; nothing stops
running. Everything here is already yours.
