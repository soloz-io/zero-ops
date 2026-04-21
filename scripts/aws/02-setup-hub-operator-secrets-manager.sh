#!/bin/bash
set -euo pipefail

# 02-setup-hub-operator-secrets-manager.sh
# Setup AWS Secrets Manager access for hub-operator
# This script creates IAM user, policy, and backs up current Infisical master keys
#
# Usage:
#   AWS_PROFILE=zerotouch-platform-admin ./scripts/aws/02-setup-hub-operator-secrets-manager.sh <environment> <cluster-name>
#
# Example:
#   AWS_PROFILE=zerotouch-platform-admin ./scripts/aws/02-setup-hub-operator-secrets-manager.sh dev hub-production

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Configuration
ENVIRONMENT="${1:-}"
CLUSTER_NAME="${2:-}"

if [[ -z "$ENVIRONMENT" ]] || [[ -z "$CLUSTER_NAME" ]]; then
    echo "Usage: $0 <environment> <cluster-name>"
    echo "Example: $0 dev hub-production"
    exit 1
fi

# Fetch Account ID dynamically
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
REGION="ap-south-1"

IAM_USER_NAME="hub-operator-secrets-manager-${ENVIRONMENT}"
IAM_POLICY_NAME="hub-operator-secrets-manager-policy-${ENVIRONMENT}"
SECRET_PATH="/hub-operator/${CLUSTER_NAME}/infisical-master-keys"
K8S_SECRET_NAME="aws-secrets-manager-credentials"
K8S_NAMESPACE="hub-platform-ops"

echo "=========================================="
echo "Hub Operator Secrets Manager Setup"
echo "=========================================="
echo "Environment: ${ENVIRONMENT}"
echo "Cluster Name: ${CLUSTER_NAME}"
echo "AWS Account: ${AWS_ACCOUNT_ID}"
echo "Region: ${REGION}"
echo "IAM User: ${IAM_USER_NAME}"
echo "Secret Path: ${SECRET_PATH}"
echo ""

# Prerequisites Check
if ! command -v aws &> /dev/null; then
    echo "❌ AWS CLI is not installed. Please install it."
    exit 1
fi

if ! command -v kubectl &> /dev/null; then
    echo "❌ kubectl is not installed. Please install it."
    exit 1
fi

# Verify AWS credentials
if ! aws sts get-caller-identity &> /dev/null; then
    echo "❌ No valid AWS Credentials found."
    echo "   Run 'aws configure' or export AWS_PROFILE"
    exit 1
fi

CALLER_IDENTITY=$(aws sts get-caller-identity)
echo "Current AWS identity: $(echo $CALLER_IDENTITY | jq -r .Arn)"
echo ""

# ------------------------------------------------------------------
# Part 1: Create IAM User
# ------------------------------------------------------------------
echo "Step 1: Creating IAM User..."

if aws iam get-user --user-name "${IAM_USER_NAME}" &>/dev/null; then
    echo "✅ IAM User ${IAM_USER_NAME} already exists"
else
    aws iam create-user --user-name "${IAM_USER_NAME}" \
        --tags "Key=Environment,Value=${ENVIRONMENT}" "Key=ManagedBy,Value=hub-operator" "Key=Purpose,Value=secrets-manager-access"
    echo "✅ Created IAM User: ${IAM_USER_NAME}"
fi

# ------------------------------------------------------------------
# Part 2: Create and Attach IAM Policy
# ------------------------------------------------------------------
echo ""
echo "Step 2: Creating IAM Policy..."

POLICY_DOCUMENT=$(cat <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SecretsManagerAccess",
      "Effect": "Allow",
      "Action": [
        "secretsmanager:GetSecretValue",
        "secretsmanager:PutSecretValue",
        "secretsmanager:CreateSecret",
        "secretsmanager:DescribeSecret",
        "secretsmanager:UpdateSecret"
      ],
      "Resource": "arn:aws:secretsmanager:${REGION}:${AWS_ACCOUNT_ID}:secret:/hub-operator/*"
    },
    {
      "Sid": "SecretsManagerList",
      "Effect": "Allow",
      "Action": [
        "secretsmanager:ListSecrets"
      ],
      "Resource": "*"
    }
  ]
}
EOF
)

POLICY_ARN="arn:aws:iam::${AWS_ACCOUNT_ID}:policy/${IAM_POLICY_NAME}"

if aws iam get-policy --policy-arn "${POLICY_ARN}" &>/dev/null; then
    echo "✅ IAM Policy ${IAM_POLICY_NAME} already exists"
    
    # Update policy with new version
    echo "   Updating policy to latest version..."
    aws iam create-policy-version \
        --policy-arn "${POLICY_ARN}" \
        --policy-document "${POLICY_DOCUMENT}" \
        --set-as-default 2>/dev/null || echo "   Policy already up to date"
else
    aws iam create-policy \
        --policy-name "${IAM_POLICY_NAME}" \
        --policy-document "${POLICY_DOCUMENT}" \
        --description "Secrets Manager access for hub-operator in ${ENVIRONMENT}"
    echo "✅ Created IAM Policy: ${IAM_POLICY_NAME}"
fi

# Attach policy to user
echo "   Attaching policy to user..."
aws iam attach-user-policy \
    --user-name "${IAM_USER_NAME}" \
    --policy-arn "${POLICY_ARN}" 2>/dev/null || echo "   Policy already attached"

# ------------------------------------------------------------------
# Part 3: Create Access Keys
# ------------------------------------------------------------------
echo ""
echo "Step 3: Creating Access Keys..."

# Check if user already has access keys
EXISTING_KEYS=$(aws iam list-access-keys --user-name "${IAM_USER_NAME}" --query 'AccessKeyMetadata[?Status==`Active`].AccessKeyId' --output text)

if [[ -n "$EXISTING_KEYS" ]]; then
    echo "⚠️  User already has active access keys:"
    echo "   ${EXISTING_KEYS}"
    echo ""
    read -p "Do you want to create a new access key? (This will require rotating the old key) [y/N]: " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Skipping access key creation. Using existing keys."
        echo "⚠️  You must manually update the Kubernetes secret with existing credentials."
        ACCESS_KEY_ID=$(echo "$EXISTING_KEYS" | head -n1)
        SECRET_ACCESS_KEY="<EXISTING_KEY_FROM_SECURE_STORAGE>"
    else
        # Create new access key
        ACCESS_KEY_JSON=$(aws iam create-access-key --user-name "${IAM_USER_NAME}")
        ACCESS_KEY_ID=$(echo "$ACCESS_KEY_JSON" | jq -r .AccessKey.AccessKeyId)
        SECRET_ACCESS_KEY=$(echo "$ACCESS_KEY_JSON" | jq -r .AccessKey.SecretAccessKey)
        echo "✅ Created new access key: ${ACCESS_KEY_ID}"
        echo ""
        echo "⚠️  IMPORTANT: Save these credentials securely!"
        echo "   Access Key ID: ${ACCESS_KEY_ID}"
        echo "   Secret Access Key: ${SECRET_ACCESS_KEY}"
        echo ""
        echo "   These credentials will be stored in Kubernetes secret: ${K8S_SECRET_NAME}"
        echo ""
    fi
else
    # Create new access key
    ACCESS_KEY_JSON=$(aws iam create-access-key --user-name "${IAM_USER_NAME}")
    ACCESS_KEY_ID=$(echo "$ACCESS_KEY_JSON" | jq -r .AccessKey.AccessKeyId)
    SECRET_ACCESS_KEY=$(echo "$ACCESS_KEY_JSON" | jq -r .AccessKey.SecretAccessKey)
    echo "✅ Created access key: ${ACCESS_KEY_ID}"
    echo ""
    echo "⚠️  IMPORTANT: Save these credentials securely!"
    echo "   Access Key ID: ${ACCESS_KEY_ID}"
    echo "   Secret Access Key: ${SECRET_ACCESS_KEY}"
    echo ""
fi

# ------------------------------------------------------------------
# Part 4: Create Kubernetes Secret
# ------------------------------------------------------------------
echo ""
echo "Step 4: Creating Kubernetes Secret..."

# Check if namespace exists
if ! kubectl get namespace "${K8S_NAMESPACE}" &>/dev/null; then
    echo "❌ Namespace ${K8S_NAMESPACE} does not exist"
    echo "   Please create the namespace first or ensure you're connected to the correct cluster"
    exit 1
fi

# Check if secret already exists
if kubectl get secret "${K8S_SECRET_NAME}" -n "${K8S_NAMESPACE}" &>/dev/null; then
    echo "⚠️  Secret ${K8S_SECRET_NAME} already exists in namespace ${K8S_NAMESPACE}"
    read -p "Do you want to update it? [y/N]: " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        kubectl delete secret "${K8S_SECRET_NAME}" -n "${K8S_NAMESPACE}"
        echo "   Deleted existing secret"
    else
        echo "Skipping Kubernetes secret creation"
        echo ""
        echo "⚠️  Manual action required: Update the secret with new credentials if needed"
        exit 0
    fi
fi

if [[ "$SECRET_ACCESS_KEY" != "<EXISTING_KEY_FROM_SECURE_STORAGE>" ]]; then
    kubectl create secret generic "${K8S_SECRET_NAME}" \
        -n "${K8S_NAMESPACE}" \
        --from-literal=AWS_ACCESS_KEY_ID="${ACCESS_KEY_ID}" \
        --from-literal=AWS_SECRET_ACCESS_KEY="${SECRET_ACCESS_KEY}" \
        --from-literal=AWS_REGION="${REGION}"
    echo "✅ Created Kubernetes secret: ${K8S_SECRET_NAME} in namespace ${K8S_NAMESPACE}"
else
    echo "⚠️  Skipping Kubernetes secret creation - using existing access keys"
    echo "   You must manually create the secret with:"
    echo "   kubectl create secret generic ${K8S_SECRET_NAME} \\"
    echo "     -n ${K8S_NAMESPACE} \\"
    echo "     --from-literal=AWS_ACCESS_KEY_ID=<YOUR_KEY_ID> \\"
    echo "     --from-literal=AWS_SECRET_ACCESS_KEY=<YOUR_SECRET_KEY> \\"
    echo "     --from-literal=AWS_REGION=${REGION}"
fi

# ------------------------------------------------------------------
# Part 5: Backup Current Infisical Master Keys
# ------------------------------------------------------------------
echo ""
echo "Step 5: Backing up current Infisical master keys..."

# Check if infisical-secrets exists
if ! kubectl get secret infisical-secrets -n hub-platform-security &>/dev/null; then
    echo "⚠️  Secret 'infisical-secrets' not found in namespace 'hub-platform-security'"
    echo "   Skipping backup. This is expected for new clusters."
    echo ""
    echo "   For existing clusters, ensure:"
    echo "   1. You're connected to the correct cluster"
    echo "   2. Infisical is deployed and running"
    echo "   3. The secret exists in the hub-platform-security namespace"
else
    # Extract current keys
    echo "   Extracting ENCRYPTION_KEY and AUTH_SECRET..."
    ENCRYPTION_KEY=$(kubectl get secret infisical-secrets -n hub-platform-security -o jsonpath='{.data.ENCRYPTION_KEY}' | base64 -d)
    AUTH_SECRET=$(kubectl get secret infisical-secrets -n hub-platform-security -o jsonpath='{.data.AUTH_SECRET}' | base64 -d)
    
    # Validate key format
    if [[ ${#ENCRYPTION_KEY} -ne 32 ]]; then
        echo "❌ ENCRYPTION_KEY is not 32 characters (got ${#ENCRYPTION_KEY})"
        echo "   This indicates a problem with the current deployment"
        exit 1
    fi
    
    if [[ ${#AUTH_SECRET} -ne 32 ]]; then
        echo "❌ AUTH_SECRET is not 32 characters (got ${#AUTH_SECRET})"
        echo "   This indicates a problem with the current deployment"
        exit 1
    fi
    
    echo "✅ Extracted keys (both are 32 characters)"
    
    # Create backup JSON
    BACKUP_JSON=$(cat <<EOF
{
  "encryptionKey": "${ENCRYPTION_KEY}",
  "authSecret": "${AUTH_SECRET}",
  "createdAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "clusterId": "${CLUSTER_NAME}",
  "version": "1",
  "backupTimestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
)
    
    # Check if secret already exists in AWS
    if aws secretsmanager describe-secret --secret-id "${SECRET_PATH}" --region "${REGION}" &>/dev/null; then
        echo "⚠️  Secret ${SECRET_PATH} already exists in AWS Secrets Manager"
        read -p "Do you want to update it? [y/N]: " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            aws secretsmanager update-secret \
                --secret-id "${SECRET_PATH}" \
                --secret-string "${BACKUP_JSON}" \
                --region "${REGION}"
            echo "✅ Updated secret in AWS Secrets Manager: ${SECRET_PATH}"
        else
            echo "Skipping AWS backup update"
        fi
    else
        # Create new secret
        aws secretsmanager create-secret \
            --name "${SECRET_PATH}" \
            --description "Infisical master keys for ${CLUSTER_NAME} (${ENVIRONMENT})" \
            --secret-string "${BACKUP_JSON}" \
            --region "${REGION}" \
            --tags "Key=Environment,Value=${ENVIRONMENT}" "Key=Cluster,Value=${CLUSTER_NAME}" "Key=ManagedBy,Value=hub-operator"
        echo "✅ Created secret in AWS Secrets Manager: ${SECRET_PATH}"
    fi
    
    # Enable KMS encryption (if not already enabled)
    echo "   Ensuring KMS encryption is enabled..."
    aws secretsmanager update-secret \
        --secret-id "${SECRET_PATH}" \
        --kms-key-id "alias/aws/secretsmanager" \
        --region "${REGION}" &>/dev/null || echo "   KMS encryption already configured"
fi

# ------------------------------------------------------------------
# Summary
# ------------------------------------------------------------------
echo ""
echo "=========================================="
echo "✅ Setup Complete!"
echo "=========================================="
echo ""
echo "Summary:"
echo "  IAM User: ${IAM_USER_NAME}"
echo "  IAM Policy: ${IAM_POLICY_NAME}"
echo "  Access Key ID: ${ACCESS_KEY_ID}"
echo "  Kubernetes Secret: ${K8S_SECRET_NAME} (namespace: ${K8S_NAMESPACE})"
echo "  AWS Secret Path: ${SECRET_PATH}"
echo "  Region: ${REGION}"
echo ""
echo "Next steps:"
echo "  1. Verify the Kubernetes secret exists:"
echo "     kubectl get secret ${K8S_SECRET_NAME} -n ${K8S_NAMESPACE}"
echo ""
echo "  2. Verify the AWS secret exists:"
echo "     aws secretsmanager get-secret-value --secret-id ${SECRET_PATH} --region ${REGION}"
echo ""
echo "  3. Proceed with hub-operator implementation"
echo ""
echo "⚠️  Security Reminders:"
echo "  - Store the Secret Access Key securely (password manager, vault, etc.)"
echo "  - Enable CloudTrail for audit logging"
echo "  - Rotate access keys periodically"
echo "  - Monitor AWS CloudWatch for unauthorized access attempts"
echo ""
