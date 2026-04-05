#!/bin/bash
set -e

# Step 1: Login
echo "=== Step 1: Login ==="
INITIAL_TOKEN=$(curl -s -X POST http://localhost:8080/api/v3/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"arun4infra@gmail.com","password":"Password@123"}' | jq -r '.accessToken')

echo "Initial token obtained: ${INITIAL_TOKEN:0:50}..."

# Step 2: Select Organization
echo -e "\n=== Step 2: Select Organization ==="
ORG_ID="f4e9e380-c176-4d61-b3d8-f1c54e816030"
TOKEN=$(curl -s -X POST http://localhost:8080/api/v3/auth/select-organization \
  -H "Authorization: Bearer $INITIAL_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"organizationId\":\"$ORG_ID\"}" | jq -r '.token')

echo "Org-scoped token obtained: ${TOKEN:0:50}..."

# Use existing IDs from database
PROJECT_ID="51b7d5bc-fde8-4c2b-81b3-8794ba301a11"
IDENTITY_ID="f531bc80-f79c-41a0-985f-ac16f5c177ee"

echo "Project ID: $PROJECT_ID"
echo "Identity ID: $IDENTITY_ID"
echo "Org ID: $ORG_ID"

# Step 3: Attach Universal Auth (THIS IS WHERE IT FAILED BEFORE)
echo -e "\n=== Step 3: Attach Universal Auth ==="
ATTACH_RESPONSE=$(curl -s -w "\nHTTP_STATUS:%{http_code}" -X POST \
  "http://localhost:8080/api/v1/auth/universal-auth/identities/$IDENTITY_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}')

HTTP_STATUS=$(echo "$ATTACH_RESPONSE" | grep "HTTP_STATUS" | cut -d: -f2)
BODY=$(echo "$ATTACH_RESPONSE" | sed '/HTTP_STATUS/d')

echo "Status: $HTTP_STATUS"
echo "Response: $BODY" | jq . 2>/dev/null || echo "$BODY"

if [ "$HTTP_STATUS" = "400" ] && echo "$BODY" | grep -q "already configured"; then
  echo "Universal Auth already attached, continuing..."
elif [ "$HTTP_STATUS" != "200" ] && [ "$HTTP_STATUS" != "201" ]; then
  echo "ERROR: Failed to attach universal auth"
  exit 1
fi

# Step 4: Generate Client Credentials
echo -e "\n=== Step 4: Generate Client Credentials ==="
CREDS_RESPONSE=$(curl -s -w "\nHTTP_STATUS:%{http_code}" -X POST \
  "http://localhost:8080/api/v1/auth/universal-auth/identities/$IDENTITY_ID/client-secrets" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}')

HTTP_STATUS=$(echo "$CREDS_RESPONSE" | grep "HTTP_STATUS" | cut -d: -f2)
BODY=$(echo "$CREDS_RESPONSE" | sed '/HTTP_STATUS/d')

echo "Status: $HTTP_STATUS"
echo "Response: $BODY" | jq . 2>/dev/null || echo "$BODY"

if [ "$HTTP_STATUS" != "200" ] && [ "$HTTP_STATUS" != "201" ]; then
  echo "ERROR: Failed to generate client credentials"
  exit 1
fi

CLIENT_ID=$(echo "$BODY" | jq -r '.clientSecretData.clientId // .clientId // empty')
CLIENT_SECRET=$(echo "$BODY" | jq -r '.clientSecret // empty')

# If clientId is not in response, get it from the identityUniversalAuth
if [ -z "$CLIENT_ID" ]; then
  # Get the clientId from the universal auth config
  UA_RESPONSE=$(curl -s -X GET \
    "http://localhost:8080/api/v1/auth/universal-auth/identities/$IDENTITY_ID" \
    -H "Authorization: Bearer $TOKEN")
  CLIENT_ID=$(echo "$UA_RESPONSE" | jq -r '.identityUniversalAuth.clientId')
fi

if [ -z "$CLIENT_ID" ] || [ -z "$CLIENT_SECRET" ]; then
  echo "ERROR: Failed to extract client credentials from response"
  echo "Full response: $BODY"
  exit 1
fi

echo "Client ID: $CLIENT_ID"
echo "Client Secret: ${CLIENT_SECRET:0:20}..."

# Step 5: Grant Project Access
echo -e "\n=== Step 5: Grant Project Access ==="
GRANT_RESPONSE=$(curl -s -w "\nHTTP_STATUS:%{http_code}" -X POST \
  "http://localhost:8080/api/v1/projects/$PROJECT_ID/memberships/identities/$IDENTITY_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"role":"admin"}')

HTTP_STATUS=$(echo "$GRANT_RESPONSE" | grep "HTTP_STATUS" | cut -d: -f2)
BODY=$(echo "$GRANT_RESPONSE" | sed '/HTTP_STATUS/d')

echo "Status: $HTTP_STATUS"
echo "Response: $BODY" | jq . 2>/dev/null || echo "$BODY"

if [ "$HTTP_STATUS" != "200" ] && [ "$HTTP_STATUS" != "201" ]; then
  echo "ERROR: Failed to grant project access"
  exit 1
fi

echo -e "\n=== Bootstrap Complete! ==="
echo "Client ID: $CLIENT_ID"
echo "Client Secret: $CLIENT_SECRET"
echo ""
echo "Next step: Create infisical-auth secret in hub-platform-ops namespace"
echo "kubectl create secret generic infisical-auth -n hub-platform-ops \\"
echo "  --from-literal=clientId=$CLIENT_ID \\"
echo "  --from-literal=clientSecret=$CLIENT_SECRET"
