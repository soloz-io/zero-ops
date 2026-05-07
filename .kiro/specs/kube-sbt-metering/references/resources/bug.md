PostgREST secret reference: Looking for hub-ory-jwt-secret in tenant namespace (should be cross-namespace reference or secret should be copied)


Root Cause Analysis:

The composition references hub-ory-jwt-secret in the Spoke cluster (tenant-app-creator namespace), but:

This secret doesn't exist in the Spoke cluster
The Hub has hydra-system-secret in platform-identity namespace
The composition never creates or copies this secret to Spoke
The Issue: PostgREST in Spoke Pool needs JWT validation but the secret is on Hub, not Spoke.