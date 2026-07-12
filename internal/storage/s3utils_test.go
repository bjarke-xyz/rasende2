package storage

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	rasendeconfig "github.com/bjarke-xyz/rasende2/internal/config"
)

// recorder captures the signed request instead of sending it.
type recorder struct{ req *http.Request }

func (r *recorder) Do(req *http.Request) (*http.Response, error) {
	r.req = req
	return nil, errors.New("not sent")
}

// The object has to land on exactly the key it is served from: the page builds
// its <img> src as S3_IMAGE_PUBLIC_BASE_URL + "/" + key, and nothing checks that
// the two agree. Store it a directory deeper and the upload still reports
// success — the only symptom is a 404 in the browser.
//
// S3_IMAGE_URL is deployed with the bucket on the end (…/public), and a path on
// the endpoint is a prefix on every key unless it is stripped. This pins that.
func TestObjectKeyIgnoresEndpointPath(t *testing.T) {
	const (
		bucket = "public"
		key    = "rasende2/articleimgs/deadbeef.png"
	)

	for _, endpoint := range []string{
		"https://acct123.r2.cloudflarestorage.com/public", // as deployed
		"https://acct123.r2.cloudflarestorage.com",        // bare host
	} {
		t.Run(endpoint, func(t *testing.T) {
			client, err := NewImageClientFromConfig(context.Background(), &rasendeconfig.Config{
				S3ImageUrl:             endpoint,
				S3ImageBucket:          bucket,
				S3ImageAccessKeyId:     "TESTKEYID",
				S3ImageSecretAccessKey: "testsecret",
			})
			if err != nil {
				t.Fatalf("construct: %v", err)
			}

			r := &recorder{}
			b, k := bucket, key
			_, _ = client.PutObject(context.Background(), &s3.PutObjectInput{
				Bucket: &b, Key: &k, Body: bytes.NewReader([]byte("PNGDATA")),
			}, func(o *s3.Options) {
				o.HTTPClient = r
				o.RetryMaxAttempts = 1
			})
			if r.req == nil {
				t.Fatal("no request was signed")
			}

			if got := r.req.URL.Path[1:]; got != key {
				t.Errorf("object key = %q, want %q\n(a %q prefix here means the image 404s at its public URL)", got, key, bucket+"/")
			}
			// R2 addresses the bucket as a subdomain, not a path.
			if want := bucket + ".acct123.r2.cloudflarestorage.com"; r.req.URL.Host != want {
				t.Errorf("host = %q, want %q", r.req.URL.Host, want)
			}
		})
	}
}

func TestEndpointHostRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"", "acct123.r2.cloudflarestorage.com", "not a url"} {
		if _, err := endpointHost(raw); err == nil {
			t.Errorf("endpointHost(%q) = nil error, want a complaint about scheme://host", raw)
		}
	}
}
