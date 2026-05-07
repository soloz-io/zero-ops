package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
)

// SecretsManagerClient provides AWS Secrets Manager operations for Infisical master key backup/restore
type SecretsManagerClient struct {
	client *secretsmanager.SecretsManager
	region string
}

// MasterKeysBackup represents the backup data structure for Infisical master keys
type MasterKeysBackup struct {
	EncryptionKey   string    `json:"encryptionKey"`
	AuthSecret      string    `json:"authSecret"`
	CreatedAt       time.Time `json:"createdAt"`
	ClusterID       string    `json:"clusterId"`
	Version         string    `json:"version"`
	BackupTimestamp time.Time `json:"backupTimestamp"`
}

// NewSecretsManagerClient creates a new AWS Secrets Manager client with static credentials
// Credentials are loaded from environment variables:
// - AWS_ACCESS_KEY_ID
// - AWS_SECRET_ACCESS_KEY
// - AWS_REGION
func NewSecretsManagerClient(ctx context.Context, region string) (*SecretsManagerClient, error) {
	// Get credentials from environment variables
	accessKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	if accessKeyID == "" || secretAccessKey == "" {
		return nil, fmt.Errorf("AWS credentials not found in environment variables")
	}

	// Create AWS session with static credentials and retry configuration
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region),
		Credentials: credentials.NewStaticCredentials(
			accessKeyID,
			secretAccessKey,
			"", // token not needed for static credentials
		),
		MaxRetries: aws.Int(5), // Configure retry with exponential backoff (max 5 attempts)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	client := secretsmanager.New(sess)

	return &SecretsManagerClient{
		client: client,
		region: region,
	}, nil
}

// BackupMasterKeys stores Infisical master keys in AWS Secrets Manager
// Path: /hub-operator/{clusterID}/infisical-master-keys
// Secret name: infisical-master-keys (no timestamps)
// KMS encryption enabled
// Accepts interface{} to match the secrets.AWSSecretsManagerClient interface
func (c *SecretsManagerClient) BackupMasterKeys(ctx context.Context, clusterID string, keys interface{}) error {
	secretName := fmt.Sprintf("/hub-operator/%s/infisical-master-keys", clusterID)
	
	// Type assert to map[string]interface{} (format used by generator.go)
	keysMap, ok := keys.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid keys format: expected map[string]interface{}, got %T", keys)
	}
	
	// Marshal backup data to JSON
	secretValue, err := json.Marshal(keysMap)
	if err != nil {
		return fmt.Errorf("failed to marshal backup data: %w", err)
	}

	// Try to update existing secret first
	_, err = c.client.UpdateSecretWithContext(ctx, &secretsmanager.UpdateSecretInput{
		SecretId:     aws.String(secretName),
		SecretString: aws.String(string(secretValue)),
		Description:  aws.String(fmt.Sprintf("Infisical master keys backup for cluster %s", clusterID)),
	})

	if err != nil {
		// If secret doesn't exist, create it
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == secretsmanager.ErrCodeResourceNotFoundException {
			// Create new secret with KMS encryption
			_, err = c.client.CreateSecretWithContext(ctx, &secretsmanager.CreateSecretInput{
				Name:         aws.String(secretName),
				SecretString: aws.String(string(secretValue)),
				Description:  aws.String(fmt.Sprintf("Infisical master keys backup for cluster %s", clusterID)),
				KmsKeyId:     aws.String("alias/aws/secretsmanager"), // Use AWS managed KMS key
				Tags: []*secretsmanager.Tag{
					{Key: aws.String("cluster"), Value: aws.String(clusterID)},
					{Key: aws.String("environment"), Value: aws.String("production")},
					{Key: aws.String("managed-by"), Value: aws.String("hub-operator")},
					{Key: aws.String("created-at"), Value: aws.String(time.Now().UTC().Format(time.RFC3339))},
				},
			})
			if err != nil {
				return fmt.Errorf("failed to create secret %s: %w", secretName, err)
			}
		} else {
			return fmt.Errorf("failed to update secret %s: %w", secretName, err)
		}
	}

	return nil
}

// RestoreMasterKeys retrieves Infisical master keys from AWS Secrets Manager
// Returns nil if secret doesn't exist (not an error - indicates first-time bootstrap)
// Returns interface{} to match the secrets.AWSSecretsManagerClient interface
func (c *SecretsManagerClient) RestoreMasterKeys(ctx context.Context, clusterID string) (interface{}, error) {
	secretName := fmt.Sprintf("/hub-operator/%s/infisical-master-keys", clusterID)

	// Get secret value
	result, err := c.client.GetSecretValueWithContext(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	})

	if err != nil {
		// If secret doesn't exist, return nil (not an error for first-time bootstrap)
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == secretsmanager.ErrCodeResourceNotFoundException {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
	}

	if result.SecretString == nil {
		return nil, fmt.Errorf("secret %s has no string value", secretName)
	}

	// Unmarshal backup data to map[string]interface{}
	var backup map[string]interface{}
	if err := json.Unmarshal([]byte(*result.SecretString), &backup); err != nil {
		return nil, fmt.Errorf("failed to unmarshal backup data from secret %s: %w", secretName, err)
	}

	return backup, nil
}