package aws

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	awsdk "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type recordingPresigner struct {
	called   int
	input    *s3.GetObjectInput
	options  s3.PresignOptions
	snapshot awsdk.Credentials
	err      error
}

func (p *recordingPresigner) PresignGetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	p.called++
	p.input = input
	for _, opt := range opts {
		opt(&p.options)
	}
	var clientOptions s3.Options
	for _, opt := range p.options.ClientOptions {
		opt(&clientOptions)
	}
	if clientOptions.Credentials != nil {
		var err error
		p.snapshot, err = clientOptions.Credentials.Retrieve(ctx)
		if err != nil {
			return nil, err
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return &v4.PresignedHTTPRequest{URL: "https://example.s3.us-west-2.amazonaws.com/folder/a%20b.txt?X-Amz-Date=" + time.Now().UTC().Format("20060102T150405Z") + "&X-Amz-Expires=" + strconv.FormatInt(int64(p.options.Expires/time.Second), 10)}, nil
}

func TestParsePresignedDuration(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"1m": time.Minute, "30m": 30 * time.Minute, "2h": 2 * time.Hour, "3d": 72 * time.Hour, "7d": 7 * 24 * time.Hour,
	} {
		got, err := ParsePresignedDuration(input)
		if err != nil || got != want {
			t.Errorf("ParsePresignedDuration(%q) = %v, %v, want %v", input, got, err, want)
		}
	}
	for _, input := range []string{"", "0m", "-1h", "1.5h", "8d", "100000000000000000000d", "5s", "1hgarbage"} {
		if _, err := ParsePresignedDuration(input); err == nil {
			t.Errorf("ParsePresignedDuration(%q) accepted invalid duration", input)
		}
	}
}

func TestPresignedDownloadUsesPreparedCredentialSnapshot(t *testing.T) {
	first := awsdk.Credentials{AccessKeyID: "FIRST", SecretAccessKey: "secret", SessionToken: "session-one", CanExpire: true, Expires: time.Now().Add(2 * time.Hour)}
	second := awsdk.Credentials{AccessKeyID: "SECOND", SecretAccessKey: "secret-two", SessionToken: "session-two"}
	reads := 0
	provider := awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
		reads++
		if reads == 1 {
			return first, nil
		}
		return second, nil
	})
	signer := &recordingPresigner{}
	client := &Client{PresignS3: signer, Credentials: provider}
	plan, err := client.PreparePresignedDownload(context.Background(), "bucket", "folder/a b.txt", "us-west-2", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if plan.NeedsShorterDuration || plan.KnownCredentialExpiry.IsZero() {
		t.Fatalf("unexpected credential limit: %+v", plan)
	}
	result, err := client.GeneratePresignedDownload(context.Background(), plan, false)
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 || signer.snapshot.AccessKeyID != "FIRST" || signer.snapshot.SessionToken != "session-one" {
		t.Fatalf("signing used different credentials: reads=%d accessKey=%q", reads, signer.snapshot.AccessKeyID)
	}
	if signer.input == nil || awsdk.ToString(signer.input.Key) != "folder/a b.txt" || signer.options.Expires != time.Hour {
		t.Fatalf("wrong signing input: %+v, duration %v", signer.input, signer.options.Expires)
	}
	var options s3.Options
	for _, opt := range signer.options.ClientOptions {
		opt(&options)
	}
	if options.Region != "us-west-2" {
		t.Fatalf("wrong region %q", options.Region)
	}
	if result.URL == "" || result.RequestedDuration != time.Hour || result.EffectiveDuration != time.Hour || result.SignedAt.IsZero() || result.ExpiresAt.Sub(result.SignedAt) != time.Hour {
		t.Fatalf("invalid result: %+v", result)
	}
}

func TestPresignedDownloadRequiresShorteningConfirmation(t *testing.T) {
	provider := awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
		return awsdk.Credentials{AccessKeyID: "TEMP", SecretAccessKey: "secret", CanExpire: true, Expires: time.Now().Add(30 * time.Minute)}, nil
	})
	signer := &recordingPresigner{}
	client := &Client{PresignS3: signer, Credentials: provider}
	plan, err := client.PreparePresignedDownload(context.Background(), "bucket", "file", "us-east-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NeedsShorterDuration || plan.AvailableDuration < 29*time.Minute || plan.AvailableDuration >= 30*time.Minute {
		t.Fatalf("unexpected available duration: %+v", plan)
	}
	if _, err := client.GeneratePresignedDownload(context.Background(), plan, false); !errors.Is(err, ErrPresignedDurationNeedsConfirmation) || signer.called != 0 {
		t.Fatalf("generated without confirmation: %v, calls=%d", err, signer.called)
	}
	result, err := client.GeneratePresignedDownload(context.Background(), plan, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.EffectiveDuration >= time.Hour || result.ExpiresAt.After(plan.KnownCredentialExpiry) {
		t.Fatalf("signed link exceeds credential limit: %+v", result)
	}
}

func TestPresignedDownloadRejectsNearExpiredCredentials(t *testing.T) {
	client := &Client{PresignS3: &recordingPresigner{}, Credentials: awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
		return awsdk.Credentials{AccessKeyID: "TEMP", SecretAccessKey: "secret", CanExpire: true, Expires: time.Now().Add(30 * time.Second)}, nil
	})}
	if _, err := client.PreparePresignedDownload(context.Background(), "bucket", "file", "us-east-1", time.Hour); !errors.Is(err, ErrPresignedCredentialsExpiring) {
		t.Fatalf("expected expiring credentials error, got %v", err)
	}
}

func TestPresignedDownloadUnknownTokenExpiry(t *testing.T) {
	client := &Client{PresignS3: &recordingPresigner{}, Credentials: awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
		return awsdk.Credentials{AccessKeyID: "TEMP", SecretAccessKey: "secret", SessionToken: "token"}, nil
	})}
	plan, err := client.PreparePresignedDownload(context.Background(), "bucket", "file", "us-east-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SessionExpiryUnknown || !plan.KnownCredentialExpiry.IsZero() || plan.NeedsShorterDuration {
		t.Fatalf("unexpected unknown-expiry status: %+v", plan)
	}
	for _, rendered := range []string{fmt.Sprintf("%v", plan), fmt.Sprintf("%+v", plan), fmt.Sprintf("%#v", plan)} {
		if strings.Contains(rendered, "secret") || strings.Contains(rendered, "token") {
			t.Fatalf("plan formatting exposed credentials")
		}
	}
}

func TestPresignedDownloadServiceValidationAndFailure(t *testing.T) {
	provider := awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
		return awsdk.Credentials{AccessKeyID: "KEY", SecretAccessKey: "secret"}, nil
	})
	signer := &recordingPresigner{err: errors.New("sign failed")}
	client := &Client{PresignS3: signer, Credentials: provider}
	for _, tc := range []struct {
		bucket, key, region string
		duration            time.Duration
	}{
		{"", "file", "us-east-1", time.Hour}, {"bucket", "", "us-east-1", time.Hour}, {"bucket", "file", "", time.Hour}, {"bucket", "file", "us-east-1", 30 * time.Second}, {"bucket", "file", "us-east-1", 8 * 24 * time.Hour},
	} {
		if _, err := client.PreparePresignedDownload(context.Background(), tc.bucket, tc.key, tc.region, tc.duration); err == nil {
			t.Errorf("accepted invalid input: %+v", tc)
		}
	}
	plan, err := client.PreparePresignedDownload(context.Background(), "bucket", "file", "us-east-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GeneratePresignedDownload(context.Background(), plan, false)
	if err == nil || !strings.Contains(err.Error(), "sign failed") {
		t.Fatalf("missing signing failure: %v", err)
	}
	if signer.called != 1 {
		t.Fatalf("signer calls = %d", signer.called)
	}
}

func TestPresignedDownloadReturnedURLCanBeParsed(t *testing.T) {
	client := &Client{PresignS3: &recordingPresigner{}, Credentials: awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
		return awsdk.Credentials{AccessKeyID: "KEY", SecretAccessKey: "secret"}, nil
	})}
	plan, err := client.PreparePresignedDownload(context.Background(), "bucket", "folder/a b.txt", "us-west-2", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.GeneratePresignedDownload(context.Background(), plan, false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result.URL)
	if err != nil || parsed.Query().Get("X-Amz-Date") == "" {
		t.Fatalf("invalid URL: %v", err)
	}
}

func TestPresignedDownloadRealSignerPreservesRegionKeyAndSessionToken(t *testing.T) {
	base := s3.NewFromConfig(awsdk.Config{
		Region: "us-east-1",
		Credentials: awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
			return awsdk.Credentials{AccessKeyID: "WRONG", SecretAccessKey: "wrong"}, nil
		}),
	})
	reads := 0
	client := &Client{
		PresignS3: s3.NewPresignClient(base),
		Credentials: awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
			reads++
			return awsdk.Credentials{AccessKeyID: "RIGHT", SecretAccessKey: "secret", SessionToken: "session-token"}, nil
		}),
	}
	key := "folder/a b-雪.txt"
	plan, err := client.PreparePresignedDownload(context.Background(), "example-bucket", key, "us-west-2", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.GeneratePresignedDownload(context.Background(), plan, false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if reads != 1 {
		t.Fatalf("credential snapshot retrieved %d times", reads)
	}
	if !strings.Contains(query.Get("X-Amz-Credential"), "RIGHT/") || !strings.Contains(query.Get("X-Amz-Credential"), "/us-west-2/s3/aws4_request") {
		t.Fatalf("wrong signing identity or region in credential scope")
	}
	if query.Get("X-Amz-Security-Token") != "session-token" {
		t.Fatal("session token missing from presigned link")
	}
	if parsed.Path != "/"+key {
		t.Fatalf("decoded object path = %q, want %q", parsed.Path, "/"+key)
	}
	if query.Get("X-Amz-Expires") != "3600" {
		t.Fatalf("signed lifetime = %q", query.Get("X-Amz-Expires"))
	}
}

func TestPresignedDownloadRealSignerPreservesCustomEndpoint(t *testing.T) {
	base := s3.NewFromConfig(awsdk.Config{
		Region:       "us-east-1",
		BaseEndpoint: awsdk.String("https://storage.example.test"),
		Credentials: awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
			return awsdk.Credentials{AccessKeyID: "KEY", SecretAccessKey: "secret"}, nil
		}),
	})
	client := &Client{
		PresignS3: s3.NewPresignClient(base),
		Credentials: awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
			return awsdk.Credentials{AccessKeyID: "KEY", SecretAccessKey: "secret"}, nil
		}),
	}
	plan, err := client.PreparePresignedDownload(context.Background(), "example-bucket", "file.txt", "us-west-2", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.GeneratePresignedDownload(context.Background(), plan, false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(parsed.Hostname(), ".storage.example.test") {
		t.Fatalf("custom endpoint missing from host %q", parsed.Hostname())
	}
}
