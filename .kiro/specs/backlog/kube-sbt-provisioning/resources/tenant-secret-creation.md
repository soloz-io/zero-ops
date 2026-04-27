# Proposal: Correct Infisical Secret Structure

## How Infisical Works

Infisical supports **folder paths** (secretPath) + **secret keys**:

- **secretPath**: `/spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator` (folder structure)
- **Secret Key**: `db-credentials` (the actual secret name)
- **Secret Value**: JSON object with properties `{"username": "...", "password": "..."}`

## What You Need to Create in Infisical UI

**Location:**
- Project: `hub-platform`
- Environment: `dev`
- Folder Path: `/spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator`

**Secret:**
- **Key**: `db-credentials`
- **Value**: JSON object with two properties:
  ```json
  {
    "username": "tenant_app-creator_user",
    "password": "8b729a04-95ac-4dfe-92ea-ad0ccebc"
  }
  ```

OR if Infisical UI doesn't support JSON, create it as two separate key-value pairs within the same secret:
- Property `username` = `tenant_app-creator_user`
- Property `password` = `8b729a04-95ac-4dfe-92ea-ad0ccebc`

## No Code Changes Needed

The current composition is already correct! It's looking for:
- Path: `/spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator/db-credentials`
- Properties: `username`, `password`

This matches Infisical's API structure where the path includes the secret key at the end.

---

**Can you create the secret in Infisical with this structure and confirm?**

✅ ESO Dynamic Path Pull: SUCCESS

ESO successfully pulled secrets from Infisical using dynamically constructed path
Path: /spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator/db-credentials
Secret created with correct username, password, and dynamically computed URL
✅ Password in URL: FIXED

URL now contains password: postgresql://tenant_app-creator_user:8b729a04-95ac-4dfe-92ea-ad0ccebc@app-creator-pooler...