- Tenant provisioning using openSBT
- SBT will generate DB secrets, create DB user and password in DB and apply ESO for secrets syncs for apps.
- Use lambda function will rotate secret in DB and in secret manager.
- Use KMS for storing infisical encryption secrets
- Use AWS Secret manager for storing secrets
- Use ESO to sync secrets to cluster
- https://www.linkedin.com/pulse/implementing-managed-rotating-secrets-kubernetes-journey-goswami-1wkdc/

- Use dual phase rotation mechanism as defined in https://infisical.com/docs/documentation/platform/secret-rotation/overview ..
- Use blue green canary as in https://medium.com/@quicksilversel/kubernetes-secrets-management-how-we-rotate-secrets-without-breaking-production-6c6ed6fcb115
- Use as reference for testing - https://oneuptime.com/blog/post/2026-02-09-secret-rotation-external-secrets-refresh/view


What's Missing
We need to define:

Rotation trigger mechanism (time-based? manual? event-driven?)
Rotation workflow (step-by-step process)
Overlap period implementation (how do we maintain both old and new credentials?)
Pooler/PostgREST credential update (how do they pick up new credentials?)
Rollback strategy (what if rotation fails?)
Monitoring and alerting (how do we know rotation succeeded/failed?)