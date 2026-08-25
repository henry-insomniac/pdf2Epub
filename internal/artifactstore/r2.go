package artifactstore

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"pdf2epub/internal/domain"
)

type Config struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Prefix          string
	HTTPClient      aws.HTTPClient
	AllowHTTP       bool
}

type Store struct {
	bucket    string
	prefix    string
	client    *s3.Client
	presigner *s3.PresignClient
}

func NewR2(config Config) (*Store, error) {
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	config.AccessKeyID = strings.TrimSpace(config.AccessKeyID)
	config.Bucket = strings.TrimSpace(config.Bucket)
	config.Prefix = strings.Trim(strings.TrimSpace(config.Prefix), "/")
	if config.Endpoint == "" || config.AccessKeyID == "" || config.SecretAccessKey == "" || config.Bucket == "" {
		return nil, errors.New("R2 endpoint, credentials and bucket are required")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && !(config.AllowHTTP && endpoint.Scheme == "http")) {
		return nil, errors.New("R2 endpoint must be an absolute HTTPS URL")
	}

	awsConfig := aws.Config{
		Region:      "auto",
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.SecretAccessKey, "")),
		HTTPClient:  config.HTTPClient,
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint.String())
		options.UsePathStyle = true
	})
	return &Store{
		bucket:    config.Bucket,
		prefix:    config.Prefix,
		client:    client,
		presigner: s3.NewPresignClient(client),
	}, nil
}

func (s *Store) Publish(ctx context.Context, jobID string, artifact domain.Artifact) (string, error) {
	if artifact.Path == "" || artifact.Name == "" || artifact.Size < 0 {
		return "", errors.New("local artifact metadata is incomplete")
	}
	if !safeKeyPart(jobID) {
		return "", errors.New("job ID contains unsafe object key characters")
	}
	file, err := os.Open(artifact.Path)
	if err != nil {
		return "", fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()

	key := jobID + ".epub"
	if s.prefix != "" {
		key = path.Join(s.prefix, key)
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(artifact.Name)})
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(s.bucket),
		Key:                aws.String(key),
		Body:               file,
		CacheControl:       aws.String("private, no-store"),
		ContentDisposition: aws.String(disposition),
		ContentLength:      aws.Int64(artifact.Size),
		ContentType:        aws.String("application/epub+zip"),
	})
	if err != nil {
		return "", fmt.Errorf("put R2 object: %w", err)
	}
	return key, nil
}

func (s *Store) SignedDownloadURL(ctx context.Context, artifact domain.Artifact, ttl time.Duration) (string, error) {
	if artifact.StorageKey == "" || ttl <= 0 {
		return "", errors.New("stored artifact key and positive URL TTL are required")
	}
	request, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(artifact.StorageKey),
	}, func(options *s3.PresignOptions) {
		options.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("presign R2 object: %w", err)
	}
	return request.URL, nil
}

func (s *Store) Delete(ctx context.Context, storageKey string) error {
	if strings.TrimSpace(storageKey) == "" {
		return errors.New("stored artifact key is required")
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	})
	if err != nil {
		return fmt.Errorf("delete R2 object: %w", err)
	}
	return nil
}

func safeKeyPart(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}
