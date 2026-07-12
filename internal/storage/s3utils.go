package storage

import (
	"context"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	rasendeconfig "github.com/bjarke-xyz/rasende2/internal/config"
)

// NewImageClientFromConfig builds the R2 client. It deliberately does not go
// through config.LoadDefaultConfig: every value that resolves is overridden here
// anyway, and the credential chain it drags in (IMDS, SSO, STS) can never run in
// this app.
func NewImageClientFromConfig(ctx context.Context, rasCfg *rasendeconfig.Config) (*s3.Client, error) {
	endpoint, err := endpointHost(rasCfg.S3ImageUrl)
	if err != nil {
		return nil, err
	}
	return s3.New(s3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(endpoint),
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     rasCfg.S3ImageAccessKeyId,
				SecretAccessKey: rasCfg.S3ImageSecretAccessKey,
			}, nil
		}),
	}), nil
}

// endpointHost reduces the configured endpoint to scheme://host, dropping any path.
//
// S3_IMAGE_URL is deployed with the bucket on the end
// (…r2.cloudflarestorage.com/public), which was harmless only because the
// endpoint resolver that used to read it threw the path away. BaseEndpoint does
// not: it keeps the path as a prefix on every key, so the same config would
// store objects at public/<key> while the page goes on serving them from <key>.
// The upload still reports success; the only symptom is a 404 in the browser.
func endpointHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid S3_IMAGE_URL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid S3_IMAGE_URL %q: want scheme://host", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}
