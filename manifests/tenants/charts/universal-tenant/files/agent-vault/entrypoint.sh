#!/bin/sh
# NOTE: keep in sync with agent-vault-entrypoint.sh
set -e

MASTER_PASSWORD=$(head -c 32 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 32)

AGENT_VAULT_MASTER_PASSWORD="$MASTER_PASSWORD" \
  /usr/local/bin/agent-vault server \
  --host 0.0.0.0 --port 14321 --mitm-port 14322 &
SERVER_PID=$!

for i in $(seq 1 30); do
  if wget -qO- http://localhost:14321/health >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

REGISTER_RESP=$(wget -qO- \
  --post-data='{"email":"sandbox@soloz.io","password":"sandbox-pw-123"}' \
  --header='Content-Type: application/json' \
  http://localhost:14321/v1/auth/register)
TOKEN=$(echo "$REGISTER_RESP" | sed 's/.*"token":"\([^"]*\)".*/\1/')

if [ -z "$TOKEN" ]; then
  LOGIN_RESP=$(wget -qO- \
    --post-data='{"email":"sandbox@soloz.io","password":"sandbox-pw-123"}' \
    --header='Content-Type: application/json' \
    http://localhost:14321/v1/auth/login)
  TOKEN=$(echo "$LOGIN_RESP" | sed 's/.*"token":"\([^"]*\)".*/\1/')
fi

if [ -z "$TOKEN" ]; then
  echo "FATAL: registration and login both failed"
  kill "$SERVER_PID"
  exit 1
fi

wget -qO- --post-data='{"name":"sandbox"}' \
  --header='Content-Type: application/json' \
  --header="Authorization: Bearer $TOKEN" \
  http://localhost:14321/v1/vaults >/dev/null 2>&1

GIT_CRED_BODY='{"vault":"sandbox","credentials":{"GITHUB_TOKEN":"'"$AGENTREGISTRY_GITHUB_TOKEN"'","GIT_USERNAME":"x-access-token"}}'
wget -qO- --post-data="$GIT_CRED_BODY" \
  --header='Content-Type: application/json' \
  --header="Authorization: Bearer $TOKEN" \
  http://localhost:14321/v1/credentials >/dev/null 2>&1

if [ -n "$AGENTREGISTRY_TAVILY_API_KEY" ]; then
  TAVILY_CRED_BODY='{"vault":"sandbox","credentials":{"TAVILY_API_KEY":"'"$AGENTREGISTRY_TAVILY_API_KEY"'"}}'
  wget -qO- --post-data="$TAVILY_CRED_BODY" \
    --header='Content-Type: application/json' \
    --header="Authorization: Bearer $TOKEN" \
    http://localhost:14321/v1/credentials >/dev/null 2>&1
fi

if [ -n "$AGENTREGISTRY_RUNPOD_AUTH" ]; then
  RUNPOD_CRED_BODY='{"vault":"sandbox","credentials":{"RUNPOD_AUTH":"'"$AGENTREGISTRY_RUNPOD_AUTH"'"}}'
  wget -qO- --post-data="$RUNPOD_CRED_BODY" \
    --header='Content-Type: application/json' \
    --header="Authorization: Bearer $TOKEN" \
    http://localhost:14321/v1/credentials >/dev/null 2>&1
fi

wget -qO- --post-data='{"services":[{"name":"github-api","host":"api.github.com","auth":{"type":"bearer","token":"GITHUB_TOKEN"}},{"name":"github-git","host":"github.com","auth":{"type":"basic","username":"GIT_USERNAME","password":"GITHUB_TOKEN"}}]}' \
  --header='Content-Type: application/json' \
  --header="Authorization: Bearer $TOKEN" \
  http://localhost:14321/v1/vaults/sandbox/services >/dev/null 2>&1

if [ -n "$AGENTREGISTRY_TAVILY_API_KEY" ]; then
  wget -qO- --post-data='{"services":[{"name":"tavily","host":"api.tavily.com","auth":{"type":"bearer","token":"TAVILY_API_KEY"}}]}' \
    --header='Content-Type: application/json' \
    --header="Authorization: Bearer $TOKEN" \
    http://localhost:14321/v1/vaults/sandbox/services >/dev/null 2>&1
fi

if [ -n "$AGENTREGISTRY_RUNPOD_AUTH" ]; then
  wget -qO- --post-data='{"services":[{"name":"runpod","host":"api.runpod.ai","auth":{"type":"bearer","token":"RUNPOD_AUTH"}}]}' \
    --header='Content-Type: application/json' \
    --header="Authorization: Bearer $TOKEN" \
    http://localhost:14321/v1/vaults/sandbox/services >/dev/null 2>&1
fi

AGENT_RESP=$(wget -qO- \
  --post-data='{"name":"sandbox-agent","role":"no-access","vaults":[{"vault_name":"sandbox","vault_role":"proxy"}]}' \
  --header='Content-Type: application/json' \
  --header="Authorization: Bearer $TOKEN" \
  http://localhost:14321/v1/agents)
AGENT_TOKEN=$(echo "$AGENT_RESP" | sed 's/.*"av_agent_token":"\([^"]*\)".*/\1/')

if [ -z "$AGENT_TOKEN" ]; then
  echo "FATAL: agent creation failed: $AGENT_RESP"
  kill "$SERVER_PID"
  exit 1
fi

wget -qO- http://localhost:14321/v1/mitm/ca.pem > /shared/agent-vault-ca.pem

PROXY_URL="http://${AGENT_TOKEN}:sandbox@localhost:14322"
cat > /shared/proxy.env <<ENVEOF
export HTTPS_PROXY="${PROXY_URL}"
export HTTP_PROXY="${PROXY_URL}"
export NO_PROXY="localhost,127.0.0.1,.svc.cluster.local,waypoint-sdk,waypoint-postgres,waypoint-redis,oranger-serve,builder-serve"
export NODE_USE_ENV_PROXY=1
export OPENCLAW_PROXY_URL="${PROXY_URL}"
export SSL_CERT_FILE=/shared/agent-vault-ca.pem
export NODE_EXTRA_CA_CERTS=/shared/agent-vault-ca.pem
export REQUESTS_CA_BUNDLE=/shared/agent-vault-ca.pem
export CURL_CA_BUNDLE=/shared/agent-vault-ca.pem
export GIT_SSL_CAINFO=/shared/agent-vault-ca.pem
export DENO_CERT=/shared/agent-vault-ca.pem
ENVEOF

touch /shared/ready

wait "$SERVER_PID"
