package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Anciank/agentTracer/server/internal/config"
)

type S3Blob struct {
	client *s3.Client
	bucket string
}

func NewS3Blob(cfg config.Config) *S3Blob {
	u, _ := url.Parse(cfg.S3Endpoint)
	client := s3.New(s3.Options{
		Region:       cfg.S3Region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		BaseEndpoint: aws.String(cfg.S3Endpoint),
		UsePathStyle: cfg.S3UsePathStyle,
		HTTPClient:   http.DefaultClient,
	})
	_ = u
	return &S3Blob{client: client, bucket: cfg.S3Bucket}
}

func (b *S3Blob) EnsureBucket(ctx context.Context) error {
	_, err := b.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(b.bucket)})
	if err == nil {
		return nil
	}
	_, _ = b.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b.bucket)})
	return nil
}

func (b *S3Blob) Put(ctx context.Context, key, contentType string, data []byte) error {
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	return err
}

func (b *S3Blob) Get(ctx context.Context, key string) ([]byte, string, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", err
	}
	ct := "application/octet-stream"
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	return data, ct, nil
}

type MemoryBlob struct {
	data map[string][]byte
	ct   map[string]string
}

func NewMemoryBlob() *MemoryBlob {
	return &MemoryBlob{data: map[string][]byte{}, ct: map[string]string{}}
}

func (m *MemoryBlob) EnsureBucket(context.Context) error { return nil }

func (m *MemoryBlob) Put(_ context.Context, key, contentType string, data []byte) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	m.data[key] = cp
	m.ct[key] = contentType
	return nil
}

func (m *MemoryBlob) Get(_ context.Context, key string) ([]byte, string, error) {
	d, ok := m.data[key]
	if !ok {
		return nil, "", fmt.Errorf("not found: %s", key)
	}
	return d, m.ct[key], nil
}
