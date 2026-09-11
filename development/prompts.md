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