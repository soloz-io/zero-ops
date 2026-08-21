> **Status note (2026-08-21).** This file is the verbatim design review that prompted the
> ADR-050 alignment work. It is kept as the original record — **two of its claims have since
> been disproven by the vendored sources, and are struck below.** Live tracking lives in
> `adr-050-alignment.md`; do not use this file as a checklist.
>
> | Item | Now |
> |---|---|
> | #1 "Idiomatic would be RFC 8693 token exchange" | **Not available.** Hydra advertises no token-exchange grant (`ory/hydra`, `oauth2/handler.go:536`); AgentGateway's `jwtSign` claims are static (`jwt_sign.rs:75-79`) so it cannot carry the user. ID-token forwarding stands as an accepted deviation; the mitigation is the BFF validating `aud` |
> | #2 Hub-terminated ingress | **Addressed.** Drove the spoke-terminated redesign — the plan now enforces OIDC at the spoke's own gateway, deleting the ClusterMesh/MCS chain entirely |
> | #3 Environment isolation | **Fixed.** Both ClusterSecretStores and `HubEnvironment.spec.secrets.infisical.environmentSlug` now resolve per environment |
> | "Static JWKS ... change-managed restart" | **Refuted.** JWKS refreshes on a schedule (15 min default, `Cache-Control` aware, 60s floor) — `agentgateway/src/resource_manager.rs:21-25,466-524`. The real gap is narrower: no `kid`-miss on-demand refresh |
> | "auth package contracts untested" | **Still true** — tracked as plan item 3a |

What's genuinely good

The BFF pattern with server-side token custody is current best practice — tokens never reach the browser, which is exactly what the IETF browser-based-apps guidance recommends. Separate OAuth clients for the browser session versus delegated access is correct. The Redis token store with a cross-replica refresh mutex is the right shape. Two-layer authz (tenant boundary, then role) is right. And the ADR discipline throughout is better than most enterprise platforms I see.

Three things I'd push back on

1. The ID token is being used as an inter-service credential. ADR-050's own text: aud = <tenant>-public-client, "Purpose: establish browser identity only" — then forwards it as Authorization: Bearer to the BFF for the BFF to validate.

That's audience confusion. The BFF is not the audience. OIDC Core and the OAuth security BCP are explicit that ID tokens are for the client and access tokens are for resource access. The practical consequence: the same credential is simultaneously valid at the gateway and at the BFF, so there's no audience separation between hops — which is the property audience restriction exists to provide. ADR-050 lists preventing "token confusion attacks" as a benefit while doing this.

Idiomatic would be RFC 8693 token exchange at the gateway, or the gateway obtaining an access token with aud = <tenant>-bff. Notably this isn't in the rejected-alternatives list — it appears unconsidered rather than decided against.

2. Hub-terminated ingress puts the management plane in the tenant data path. Every tenant browser request lands on the cluster running CAPI, Crossplane, ArgoCD and Infisical — the PKI root and every platform secret — then traverses ClusterMesh to a pod on a NAT'd home worker.

Three consequences: the hub becomes a single point of failure for every tenant; tenant traffic shares a blast radius with the platform's root of trust; and every request takes a cross-cluster hop that must not fail.

The underlying requirement is legitimate — the BFF must not be publicly reachable except through an authenticating gateway. But the design conflates policy enforcement point with network path. The idiomatic answer is to enforce the same OIDC/JWT policy at the spoke's own gateway — Cilium Gateway already runs there and Envoy does JWT validation and ext_authz natively — so traffic terminates where the workload lives and the management cluster stays out of the data plane. That would also delete the entire ClusterMesh/MCS prerequisite chain, which is currently the largest and least-proven part of this plan.

To be clear: the plan I've written implements ADR-050 as you directed. This is feedback on the ADR, not a proposal to deviate.

3. Environment isolation is nominal, and I'd fix this before either of the above. manifests/spoke/spoke-catalog/infra/cluster-secret-store.yaml:14 hardcodes environmentSlug: "dev" for every spoke in every environment, and base hubenvironment.yaml does the same. ADR-037 mandates environment-bound hubs, but as written stg and prod would read dev's secrets. Meanwhile --environment defaults to prod while the only hub that exists is dev. For an enterprise platform that's a more serious gap than the token semantics.

Smaller, still real

- The hub pod CIDR is a Go string literal. A day-0 topology input that should be a parameter — and it's the reason a full rebuild is required at all.
- zero-ops-auth is Hono-only. A platform-wide auth primitive usually ships a framework-agnostic core with thin adapters; this locks every tenant to one framework.
- Static JWKS in AgentGateway turns Hydra signing-key rotation into a change-managed restart rather than a routine operation.
- The auth package's contracts are untested — JwtValidator, OidcClient, RedisTokenStore, and all three middlewares. Those are precisely the things tenants are told to depend on.

If I were prioritising: fix the BFF auth bypass today, fix environment isolation next, then revisit the ID-token-as-credential decision before more tenants integrate against it — changing an audience contract gets harder with every consumer.
