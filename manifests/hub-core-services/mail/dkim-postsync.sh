#!/bin/sh
set -euo pipefail

# DKIM PostSync Job — configures Stalwart DKIM signing and creates DNS TXT record.
# Runs as ArgoCD PostSync Hook after Stalwart deployment.

STALWART_URL="${STALWART_URL:-https://mail.nutgraf.in}"
STALWART_USER="${STALWART_USERNAME:-admin}"
STALWART_PASS="$(cat /etc/stalwart-admin/ADMIN_SECRET)"
DKIM_SELECTOR="${DKIM_SELECTOR:-s1}"
DKIM_DOMAIN="${DKIM_DOMAIN:-nutgraf.in}"
PRIVATE_KEY_PATH="/etc/stalwart-dkim/dkim-private.pem"
HETZNER_API_KEY="$(cat /etc/hetzner-dns/api-key)"
PUBLIC_KEY="$(cat /etc/stalwart-dkim-public/dkim-public-key)"
RECORD_NAME="${DKIM_SELECTOR}._domainkey.${DKIM_DOMAIN}"

log() { echo "[dkim-postsync] $(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }
die() { log "FATAL: $*"; exit 1; }

# ── Wait for Stalwart ──
log "Waiting for Stalwart API at ${STALWART_URL}..."
for i in $(seq 1 30); do
  if curl -sk -o /dev/null -w '%{http_code}' "${STALWART_URL}/api" | grep -qE '^(200|401)$'; then
    log "Stalwart API is ready"
    break
  fi
  [ "$i" -eq 30 ] && die "Stalwart API not ready after 30 attempts"
  sleep 5
done

# ── Authenticate with Stalwart ──
log "Authenticating with Stalwart..."
AUTH_RESP="$(curl -sk -X POST "${STALWART_URL}/api" \
  -H 'Content-Type: application/json' \
  -d "{
    \"method\": \"authenticate\",
    \"params\": { \"username\": \"${STALWART_USER}\", \"secret\": \"${STALWART_PASS}\" }
  }")"

TOKEN="$(echo "$AUTH_RESP" | sed -n 's/.*"access-token":"\([^"]*\)".*/\1/p')"
[ -z "$TOKEN" ] && die "Failed to authenticate with Stalwart"

# ── Check if signature exists (idempotency) ──
log "Checking existing DKIM signatures..."
EXISTING="$(curl -sk -X POST "${STALWART_URL}/api" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer ${TOKEN}" \
  -d "{
    \"methodCalls\": [[\"x:DkimSignature/query\", {\"filter\": {\"domain\": \"${DKIM_DOMAIN}\"}}, \"q\"]],
    \"using\": [\"urn:ietf:params:jmap:core\", \"urn:stalwart:jmap\"]
  }")"

if echo "$EXISTING" | grep -q '"ids"'; then
  SIG_ID="$(echo "$EXISTING" | sed -n 's/.*"ids":\["\([^"]*\)".*/\1/p')"
  if [ -n "$SIG_ID" ]; then
    log "DKIM signature already exists (id: ${SIG_ID}), skipping Stalwart configuration"
    SKIP_STALWART=true
  fi
fi

# ── Create DKIM signature ──
if [ "${SKIP_STALWART:-false}" != "true" ]; then
  PRIVATE_KEY="$(cat "$PRIVATE_KEY_PATH")"
  log "Creating DKIM signature for ${DKIM_DOMAIN} selector ${DKIM_SELECTOR}..."
  CREATE_RESP="$(curl -sk -X POST "${STALWART_URL}/api" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer ${TOKEN}" \
    -d "{
      \"methodCalls\": [[\"x:DkimSignature/set\", {
        \"create\": {\"dkim-1\": {
          \"@type\": \"Dkim1Ed25519Sha256\",
          \"domain\": \"${DKIM_DOMAIN}\",
          \"selector\": \"${DKIM_SELECTOR}\",
          \"privateKey\": {\"@type\": \"Text\", \"secret\": $(echo "$PRIVATE_KEY" | sed 's/"/\\"/g' | sed ':a;N;$!ba;s/\n/\\n/g' | sed 's/^/"/' | sed 's/$/"/')}
        }}
      }, \"c1\"]],
      \"using\": [\"urn:ietf:params:jmap:core\", \"urn:stalwart:jmap\"]
    }")"

  if echo "$CREATE_RESP" | grep -q '"created"'; then
    log "DKIM signature created successfully"
  else
    die "Failed to create DKIM signature: ${CREATE_RESP}"
  fi
fi

# ── Discover Hetzner zone ID ──
log "Discovering Hetzner zone for ${DKIM_DOMAIN}..."
ZONES_RESP="$(curl -s "https://dns.hetzner.com/api/v1/zones" \
  -H "Auth-API-Token: ${HETZNER_API_KEY}")"

ZONE_ID="$(echo "$ZONES_RESP" | sed -n "s/.*\"id\":\"\([^\"]*\)\",\"name\":\"${DKIM_DOMAIN}\".*/\1/p" | head -1)"
[ -z "$ZONE_ID" ] && die "Zone not found for ${DKIM_DOMAIN}"

# ── Check existing TXT record ──
DKIM_VALUE="v=DKIM1; k=ed25519; p=${PUBLIC_KEY}"
log "Checking existing TXT record for ${RECORD_NAME}..."
RECORDS_RESP="$(curl -s "https://dns.hetzner.com/api/v1/records?zone_id=${ZONE_ID}&name=${RECORD_NAME}&type=TXT" \
  -H "Auth-API-Token: ${HETZNER_API_KEY}")"

EXISTING_RECORD_ID="$(echo "$RECORDS_RESP" | sed -n 's/.*"records":\[\{"id":\("\([^"]*\)"\).*/\2/p' | head -1)"
EXISTING_VALUE="$(echo "$RECORDS_RESP" | sed -n 's/.*"records":\[[^]]*"value":\("\([^"]*\)"\).*/\2/p' | head -1)"

if [ -n "$EXISTING_RECORD_ID" ] && [ "$EXISTING_VALUE" = "$DKIM_VALUE" ]; then
  log "DNS record already correct: ${RECORD_NAME} TXT ${DKIM_VALUE}"
  exit 0
fi

# ── Create or update DNS record ──
if [ -n "$EXISTING_RECORD_ID" ]; then
  log "Updating DNS record: ${RECORD_NAME}"
  curl -s -X PUT "https://dns.hetzner.com/api/v1/records/${EXISTING_RECORD_ID}" \
    -H "Auth-API-Token: ${HETZNER_API_KEY}" \
    -H 'Content-Type: application/json' \
    -d "{\"value\": \"${DKIM_VALUE}\", \"ttl\": 3600}" > /dev/null
else
  log "Creating DNS record: ${RECORD_NAME}"
  curl -s -X POST "https://dns.hetzner.com/api/v1/records" \
    -H "Auth-API-Token: ${HETZNER_API_KEY}" \
    -H 'Content-Type: application/json' \
    -d "{\"zone_id\": \"${ZONE_ID}\", \"type\": \"TXT\", \"name\": \"${RECORD_NAME}\", \"value\": \"${DKIM_VALUE}\", \"ttl\": 3600}" > /dev/null
fi

log "DNS record reconciled: ${RECORD_NAME} TXT ${DKIM_VALUE}"
log "DKIM PostSync complete"
