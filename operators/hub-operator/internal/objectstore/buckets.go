// Package objectstore ensures the object-storage buckets the platform's stores
// write to exist (ADR-078 addendum 5 §4: one log bucket per cluster).
//
// Loki does not create its bucket, and a missing bucket is not a configuration
// error Loki reports at start: it fails at the first flush, minutes later, far
// from the cause. The hub-operator already holds the box's object-storage
// credentials, so it creates each cluster's bucket before that cluster's Loki
// needs it.
package objectstore

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client is an S3-compatible endpoint with the box's credentials.
type Client struct {
	s3     *minio.Client
	region string
}

// New connects to endpoint (a host, without scheme) over TLS.
func New(endpoint, region, accessKeyID, secretAccessKey string) (*Client, error) {
	if endpoint == "" || region == "" || accessKeyID == "" || secretAccessKey == "" {
		return nil, fmt.Errorf("object storage needs an endpoint, a region and both keys; one is empty")
	}
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: true,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("object storage client for %s: %w", endpoint, err)
	}
	return &Client{s3: c, region: region}, nil
}

// EnsureBucket creates name when it does not exist. It reports whether it
// created it. A bucket that exists is left exactly as it is.
func (c *Client) EnsureBucket(ctx context.Context, name string) (bool, error) {
	exists, err := c.s3.BucketExists(ctx, name)
	if err != nil {
		return false, fmt.Errorf("checking bucket %s: %w", name, err)
	}
	if exists {
		return false, nil
	}
	if err := c.s3.MakeBucket(ctx, name, minio.MakeBucketOptions{Region: c.region}); err != nil {
		return false, fmt.Errorf("creating bucket %s: %w", name, err)
	}
	return true, nil
}

// LogsBucket is the name of a cluster's log bucket. Loki's configuration names
// the same bucket (templated-fields: `<cluster>-logs`); the two must agree.
func LogsBucket(cluster string) string {
	return cluster + "-logs"
}
