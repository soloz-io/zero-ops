
# Hub boostratp

## Cluster Teardown

./scripts/hub-bootstrap.sh dev --teardown --yes

## Fresh Bootstrap

Sequenced (default) — boundaries activated one at a time in phase order:

./scripts/hub-bootstrap.sh \
  --name hub-hybrid-dev \
  --provider hybrid \
  --region hel1 \
  --environment dev \
  --gating sequenced \
  --spoke spoke-pool-hybrid-dev-01 \
  --home-worker-enabled \
  --tailnet-name taila4c44b.ts.net \
  --bundle-version 0.1.3

Converged (ADR-055) — all boundaries reconcile at once, red-then-green:

./scripts/hub-bootstrap.sh \
  --name hub-hybrid-dev \
  --provider hybrid \
  --region hel1 \
  --environment dev \
  --gating converged \
  --spoke spoke-pool-hybrid-dev-01 \
  --home-worker-enabled \
  --tailnet-name taila4c44b.ts.net

`--gating` defaults to `sequenced`, so omitting it gives the flow this script has
always run. Mode and environment are independent — any environment can be
created in either mode. Converged trades ordering guarantees for creation speed:
every Application is created at once and retries until its dependencies exist, so
a failure points at an Application rather than at a named phase.

## Release

- gh release create v0.1.0 --generate-notes
- gh run watch

- ./bin/hub reseed --kubeconfig k8-secrets/kubeconfig/hub-hybrid-dev.kubeconfig \
  --bundle-version 0.1.6     

- KUBECONFIG=k8-secrets/kubeconfig/hub-hybrid-dev.kubeconfig \                   
  kubectl get app platform-database -n platform-ops \
  -o jsonpath='{.spec.source.helm.values}{"\n"}'

### What the run should do:

1. Package 33 components, 5 provider charts, 8 tenant + catalogue charts, and the bundle chart — 42 total
2. Refuse if any portability reference reappears (passes on main right now)
3. Assert each chart renders exactly what ArgoCD applies today — 28 verified identical locally
4. Build four CLI binaries with the version injected, and fail if the built CLI doesn't report 0.1.0
5. Push to oci://ghcr.io/soloz-io/charts and attach the binaries to the release

- Make the GHCR packages public — ADR-063 requires a tenant to mirror without asking permission, and ArgoCD has no OCI credential.

## Download the released CLI

gh release download v0.1.9 --pattern 'soloz-darwin-arm64' --output soloz --clobber
chmod +x soloz

## Scaffold a real tenant repo 

./soloz tenant scaffold --tenant tenant1 --org soloz-io --domain tenant1.nutgraf.in

## Boundary activation

Which boundaries are open (activation is not reported as ArgoCD drift — a closed
boundary looks idle, not failed):

kubectl get appproject -n platform-ops -o custom-columns=\
NAME:.metadata.name,INACTIVE:.spec.syncWindows | grep boundary-

Open one by hand if a run was interrupted:

kubectl patch appproject boundary-03 -n platform-ops \
  --type merge -p '{"spec":{"syncWindows":null}}'

# Provision workers

## Dell Hub VM
./scripts/hybrid/provision-flatcar-worker.sh --cluster hub
./scripts/hybrid/provision-flatcar-worker.sh --node 1

## Dell Spoke VM
./scripts/hybrid/provision-flatcar-worker.sh --cluster spoke
./scripts/hybrid/provision-flatcar-worker.sh --node 2

## Prompts

-------
report back the current status update on changes made nd findings so that i can provide feedback.
keep the feedback loop open.

-------
i want only proper idiomatic enterprise grade fixes. No temporary workaround shall be considred as a fix.
make sure all adhoc changes are codified in respective manifest files following gitops principles. so that fixes dont get lost in commands.
Better approach is to make changes to manifest files and apply the files to cluster to verify its working.
it is important that any adhoc changes you make to cluster must be updated in codebase so that it is not lost and we end up again same issues in future. 

cluster access:
Hub - zero-ops/k8-secrets/kubeconfig/hub-hybrid-dev.kubeconfig
Spoke -  KUBECONFIG=/tmp/spoke-fresh.kubeconfig

------
# ADR Critic thread:
read the ADR and underatnd the expectation of the platform. let me know once you are ready to understand the proposal from the team.

You should first get the assumptions clarified from the existing codebase. dont ask for the files. ask for the details that you need to verify. I will verify and provide you the current status of codebase. Then you should find for ambiguities or gaps in the proposal? Only approve if the design is idiomatic enterprise grade.

Note: Do not provide critic just fr the sake. Your critic must be a really valid critic that needs addressing. 

Let me know when u r ready to take team proposal.

----------------------------------------
# Implementation Plan thread:
your task is not to implement. you have to report back with a implementation plan that can be used by another agent for implmentation. Now go through waypoint/packages/frontend/ and report back. The idea is to use the nodeeditor feature to display the iframe like in attached images. 

do not assume and craete a plan. only proceed when i ask u to.

-------------------------

your task is not to implement. you have to report back with a implementation plan that can be used by another agent for implmentation. cluster access:
Hub - zero-ops/k8-secrets/kubeconfig/hub-hybrid-dev.kubeconfig
Spoke -  KUBECONFIG=/tmp/spoke-fresh.kubeconfig ......... find why http://waypoint.nutgrafin is not accessble from browser. read zero-ops/docs/adr/046-hybrid-provider-home-worker.mdfirst. waypoint workloads run on flatcar node 1 worker node which is created by script -zero-ops/scripts/hybrid/provision-flatcar-worker.sh 
-----------------------------------------

i told you to never run any scripts. you just debug and fix tehissue. Report back when u need ur changes to be retested or image to be buildand deployed

--------------
I want you to evaluate the business model and the ADRs. Only approve if this idiomatic for the enterprise grade platform we are building. Ask for clarification if you see gaps and need more clarifty before gicing verdict. You have right to both approve and reject.