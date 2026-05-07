package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/sts"
)

// IAMClient provides AWS IAM operations for hub-operator setup
type IAMClient struct {
	iam       *iam.IAM
	sts       *sts.STS
	accountID string
	region    string
}

// IAMUserResult contains the created IAM user details
type IAMUserResult struct {
	UserName        string
	AccessKeyID     string
	SecretAccessKey string
	PolicyARN       string
}

// NewIAMClient creates a new AWS IAM client with admin credentials
// Uses credentials from AWS CLI profile or environment variables
func NewIAMClient(ctx context.Context, region string) (*IAMClient, error) {
	// Create AWS session with default credential chain
	// This will use AWS_PROFILE, AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, or instance profile
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	iamClient := iam.New(sess)
	stsClient := sts.New(sess)

	// Get caller identity to verify credentials and fetch account ID
	identity, err := stsClient.GetCallerIdentityWithContext(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to get caller identity (verify AWS credentials): %w", err)
	}

	return &IAMClient{
		iam:       iamClient,
		sts:       stsClient,
		accountID: *identity.Account,
		region:    region,
	}, nil
}

// CreateHubOperatorUser creates IAM user, policy, and access keys for hub-operator
// Returns IAMUserResult with credentials or error
func (c *IAMClient) CreateHubOperatorUser(ctx context.Context, environment string) (*IAMUserResult, error) {
	userName := fmt.Sprintf("hub-operator-secrets-manager-%s", environment)
	policyName := fmt.Sprintf("hub-operator-secrets-manager-policy-%s", environment)

	fmt.Printf("Creating IAM user: %s\n", userName)

	// Step 1: Create IAM user (idempotent)
	_, err := c.iam.CreateUserWithContext(ctx, &iam.CreateUserInput{
		UserName: aws.String(userName),
		Tags: []*iam.Tag{
			{Key: aws.String("Environment"), Value: aws.String(environment)},
			{Key: aws.String("ManagedBy"), Value: aws.String("hub-operator")},
			{Key: aws.String("Purpose"), Value: aws.String("secrets-manager-access")},
			{Key: aws.String("CreatedAt"), Value: aws.String(time.Now().UTC().Format(time.RFC3339))},
		},
	})

	if err != nil {
		// Ignore if user already exists
		if awsErr, ok := err.(awserr.Error); !ok || awsErr.Code() != iam.ErrCodeEntityAlreadyExistsException {
			return nil, fmt.Errorf("failed to create IAM user: %w", err)
		}
		fmt.Printf("✓ IAM user %s already exists\n", userName)
	} else {
		fmt.Printf("✓ Created IAM user: %s\n", userName)
	}

	// Step 2: Create IAM policy (idempotent)
	policyDocument := c.buildSecretsManagerPolicy()
	policyARN := fmt.Sprintf("arn:aws:iam::%s:policy/%s", c.accountID, policyName)

	_, err = c.iam.CreatePolicyWithContext(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String(policyName),
		PolicyDocument: aws.String(policyDocument),
		Description:    aws.String(fmt.Sprintf("Secrets Manager access for hub-operator in %s", environment)),
	})

	if err != nil {
		// If policy exists, update it with new version
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == iam.ErrCodeEntityAlreadyExistsException {
			fmt.Printf("✓ IAM policy %s already exists, updating to latest version...\n", policyName)
			
			// Create new policy version
			_, err = c.iam.CreatePolicyVersionWithContext(ctx, &iam.CreatePolicyVersionInput{
				PolicyArn:      aws.String(policyARN),
				PolicyDocument: aws.String(policyDocument),
				SetAsDefault:   aws.Bool(true),
			})
			if err != nil {
				// Ignore if policy is already up to date
				if awsErr, ok := err.(awserr.Error); !ok || awsErr.Code() != iam.ErrCodeLimitExceededException {
					return nil, fmt.Errorf("failed to update IAM policy: %w", err)
				}
				fmt.Printf("✓ IAM policy already at latest version\n")
			} else {
				fmt.Printf("✓ Updated IAM policy to latest version\n")
			}
		} else {
			return nil, fmt.Errorf("failed to create IAM policy: %w", err)
		}
	} else {
		fmt.Printf("✓ Created IAM policy: %s\n", policyName)
	}

	// Step 3: Attach policy to user (idempotent)
	_, err = c.iam.AttachUserPolicyWithContext(ctx, &iam.AttachUserPolicyInput{
		UserName:  aws.String(userName),
		PolicyArn: aws.String(policyARN),
	})
	if err != nil {
		// Ignore if policy is already attached
		if awsErr, ok := err.(awserr.Error); !ok || awsErr.Code() != iam.ErrCodeNoSuchEntityException {
			return nil, fmt.Errorf("failed to attach policy to user: %w", err)
		}
	}
	fmt.Printf("✓ Attached policy to user\n")

	// Step 4: Check for existing access keys
	existingKeys, err := c.iam.ListAccessKeysWithContext(ctx, &iam.ListAccessKeysInput{
		UserName: aws.String(userName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list access keys: %w", err)
	}

	// Filter active keys
	var activeKeys []*iam.AccessKeyMetadata
	for _, key := range existingKeys.AccessKeyMetadata {
		if *key.Status == "Active" {
			activeKeys = append(activeKeys, key)
		}
	}

	// If user already has active keys, return error with instructions
	if len(activeKeys) > 0 {
		return nil, fmt.Errorf("IAM user %s already has %d active access key(s). Please either:\n"+
			"  1. Use existing credentials (retrieve from secure storage)\n"+
			"  2. Delete old keys via AWS Console and re-run this command\n"+
			"  3. Rotate keys using: aws iam delete-access-key --user-name %s --access-key-id %s",
			userName, len(activeKeys), userName, *activeKeys[0].AccessKeyId)
	}

	// Step 5: Create new access key
	fmt.Printf("Creating new access key for user...\n")
	accessKeyResult, err := c.iam.CreateAccessKeyWithContext(ctx, &iam.CreateAccessKeyInput{
		UserName: aws.String(userName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create access key: %w", err)
	}

	fmt.Printf("✓ Created access key: %s\n", *accessKeyResult.AccessKey.AccessKeyId)

	return &IAMUserResult{
		UserName:        userName,
		AccessKeyID:     *accessKeyResult.AccessKey.AccessKeyId,
		SecretAccessKey: *accessKeyResult.AccessKey.SecretAccessKey,
		PolicyARN:       policyARN,
	}, nil
}

// buildSecretsManagerPolicy returns the IAM policy document for Secrets Manager access
func (c *IAMClient) buildSecretsManagerPolicy() string {
	policy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Sid":    "SecretsManagerAccess",
				"Effect": "Allow",
				"Action": []string{
					"secretsmanager:GetSecretValue",
					"secretsmanager:PutSecretValue",
					"secretsmanager:CreateSecret",
					"secretsmanager:DescribeSecret",
					"secretsmanager:UpdateSecret",
					"secretsmanager:TagResource",
				},
				"Resource": fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:/hub-operator/*", c.region, c.accountID),
			},
			{
				"Sid":      "SecretsManagerList",
				"Effect":   "Allow",
				"Action":   []string{"secretsmanager:ListSecrets"},
				"Resource": "*",
			},
		},
	}

	policyJSON, _ := json.Marshal(policy)
	return string(policyJSON)
}

// CreateHubOperatorUserWithAdminCreds creates IAM user using explicit admin credentials
// This is used when AWS_PROFILE is not available
func CreateHubOperatorUserWithAdminCreds(ctx context.Context, adminAccessKeyID, adminSecretAccessKey, region, environment string) (*IAMUserResult, error) {
	// Create AWS session with explicit admin credentials
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region),
		Credentials: credentials.NewStaticCredentials(
			adminAccessKeyID,
			adminSecretAccessKey,
			"",
		),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	iamClient := iam.New(sess)
	stsClient := sts.New(sess)

	// Get caller identity to verify credentials and fetch account ID
	identity, err := stsClient.GetCallerIdentityWithContext(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to get caller identity (verify AWS credentials): %w", err)
	}

	client := &IAMClient{
		iam:       iamClient,
		sts:       stsClient,
		accountID: *identity.Account,
		region:    region,
	}

	return client.CreateHubOperatorUser(ctx, environment)
}
