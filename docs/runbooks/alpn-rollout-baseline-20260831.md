# Cilium ALPN rollout — pre-change baseline

Captured 2026-08-31T07:23:29Z on hub-hybrid-dev, BEFORE enabling
`enable-gateway-api-alpn` (commit b241ecc2). Purpose: after the Cilium
restart, distinguish "ALPN fixed" from "the restart caused an unrelated
dataplane regression". Anything already broken here is not caused by the rollout.

## ALPN (the thing being changed)
```
No ALPN negotiated
    Protocol  : TLSv1.3
```

## Gateway + routes
```
hub-gateway   True   http,infisical,api,auth,console,argocd,mail,smtp
argocd http-to-https 
```

## Cilium health
```
cilium-ntdx5   true   0
cilium-q2nbp   true   0
cilium-envoy-z7qh9   true   0
cilium-envoy-zz2xh   true   0
```

## External endpoints (expected AFTER rollout: unchanged)
```
argocd.dev.nutgraf.in            200
```

## ArgoCD reachability
```
argocd-agent-principal-9f5b84cdc-n274b               0/1      CrashLoopBackOff
argocd-application-controller-0                      1/1      Running
argocd-applicationset-controller-7d9b8ffd5b-f725c    1/1      Running
argocd-dex-server-74bb744747-kw9kq                   1/1      Running
argocd-notifications-controller-58487db49b-khvsz     1/1      Running
argocd-redis-745479dd6d-7657v                        1/1      Running
argocd-repo-server-d7985b4d-m76p2                    1/1      Running
argocd-server-6cbfbbd444-nn2pc                       1/1      Running
```

## Known-broken BEFORE the change (do not attribute to the rollout)
- \`argocd login\` fails: no ALPN -> CLI downgrades to http -> 404 on session.SessionService/Create. This is what the rollout fixes.
- Most hub Applications report OutOfSync (pre-existing drift, unrelated).
