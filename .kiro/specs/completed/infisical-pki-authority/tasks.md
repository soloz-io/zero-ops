# Infisical PKI Authority — Tasks

## Gate P0 (all completed 2026-08-19)

- [x] Determine ownership of 65.109.41.89 (hcloud LB, CCM-owned, GitOps Service)
- [x] Prove backend determinism (single pod .80; reqIds + dual probe)
- [x] Identify DB/Redis authority (CNPG in-cluster; Redis via infisical-secrets)
- [x] Prove PKI signing end-to-end (hub CR READY; spoke CRs READY after fix)
- [x] Record P0 disposition (requirements.md + design.md)
- [x] Fix root cause permanently (operator renderer + regression test)
- [x] Classify 422/500 as test artifacts; close historical 401 string

## Disposition actions

- [x] Issuer URL migration: NOT NEEDED — no change
- [x] Public DNS: KEEP — no change
- [x] Wave 2 CNPG cutover: NO-GO (tracked)
- [ ] HA track: separate spec (future)
- [ ] 10.244.1.176 investigation: separate spec (future)