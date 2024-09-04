package storage

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	rasendeconfig "github.com/bjarke-xyz/rasende2/internal/config"
)

func NewImageClientFromConfig(ctx context.Context, rasCfg *rasendeconfig.Config) (*s3.Client, error) {
	r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL: rasCfg.S3ImageUrl,
		}, nil
	})
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithEndpointResolverWithOptions(r2Resolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(rasCfg.S3ImageAccessKeyId, rasCfg.S3ImageSecretAccessKey, "")),
		config.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load r2 config: %w", err)
	}
	client := s3.NewFromConfig(cfg)
	return client, nil
}
