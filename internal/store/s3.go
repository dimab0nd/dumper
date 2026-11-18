package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/dimab0nd/dumper/internal/config"
)

const deleteBatch = 1000

// Store uploads dumps and deletes expired objects.
type Store struct {
	client   *awss3.Client
	uploader *transfermanager.Client
	bucket   string
}

// New builds an S3 client. Credentials come from the config or from
// AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY.
func New(cfg config.S3Config) (*Store, error) {
	keyID, secret, err := credentialsFrom(cfg)
	if err != nil {
		return nil, err
	}
	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(keyID, secret, ""),
		// Compatible stores often reject the SDK's default flexible checksums.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		if endpoint := strings.TrimRight(cfg.Endpoint, "/"); endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})
	return &Store{
		client: client,
		// Objects larger than 16 MiB are uploaded as 8 MiB multipart parts.
		uploader: transfermanager.New(client),
		bucket:   cfg.Bucket,
	}, nil
}

func credentialsFrom(cfg config.S3Config) (string, string, error) {
	if cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
		if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
			return "", "", errors.New("s3 access_key_id and secret_access_key must both be set")
		}
		return cfg.AccessKeyID, cfg.SecretAccessKey, nil
	}
	keyID := os.Getenv("AWS_ACCESS_KEY_ID")
	secret := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if keyID == "" || secret == "" {
		return "", "", errors.New("s3 credentials are missing: set s3.access_key_id and s3.secret_access_key or AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}
	return keyID, secret, nil
}

// HeadBucket checks that the bucket is reachable with these credentials.
func (s *Store) HeadBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &awss3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err != nil {
		return fmt.Errorf("head bucket %s: %w", s.bucket, err)
	}
	return nil
}

// Upload writes body to key. A seekable body can be retried and sent as multipart.
func (s *Store) Upload(ctx context.Context, key string, body io.Reader) error {
	_, err := s.uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	return nil
}

// DeleteOlderThan removes objects under prefix whose LastModified is before cutoff.
func (s *Store) DeleteOlderThan(ctx context.Context, prefix string, cutoff time.Time) (int, error) {
	var keys []string
	paginator := awss3.NewListObjectsV2Paginator(s.client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil || obj.LastModified == nil {
				continue
			}
			if obj.LastModified.Before(cutoff) {
				keys = append(keys, *obj.Key)
			}
		}
	}

	deleted := 0
	for start := 0; start < len(keys); start += deleteBatch {
		end := start + deleteBatch
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[start:end]
		ids := make([]types.ObjectIdentifier, len(chunk))
		for i, key := range chunk {
			ids[i] = types.ObjectIdentifier{Key: aws.String(key)}
		}
		out, err := s.client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{
				Objects: ids,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return deleted, fmt.Errorf("delete %s: %w", prefix, err)
		}
		if len(out.Errors) > 0 {
			return deleted, fmt.Errorf("delete %s: %s", aws.ToString(out.Errors[0].Key), aws.ToString(out.Errors[0].Message))
		}
		deleted += len(chunk)
	}
	return deleted, nil
}
