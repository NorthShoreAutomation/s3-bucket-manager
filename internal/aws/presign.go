package aws

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	awsdk "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const MaxPresignedDuration = 7 * 24 * time.Hour

var (
	ErrPresignedDurationNeedsConfirmation = errors.New("available credential lifetime is shorter than the requested link lifetime")
	ErrPresignedCredentialsExpiring       = errors.New("credentials must be renewed before generating a link")
)

type PresignGetObjectAPI interface {
	PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

type PresignedDownloadPlan struct {
	Bucket                string
	Key                   string
	Region                string
	RequestedDuration     time.Duration
	AvailableDuration     time.Duration
	KnownCredentialExpiry time.Time
	NeedsShorterDuration  bool
	SessionExpiryUnknown  bool
	credentials           awsdk.Credentials
}

// String omits the signing credential snapshot from formatted output.
func (p *PresignedDownloadPlan) String() string {
	if p == nil {
		return "PresignedDownloadPlan<nil>"
	}
	return fmt.Sprintf("PresignedDownloadPlan{requested:%s available:%s needsShorter:%t}", p.RequestedDuration, p.AvailableDuration, p.NeedsShorterDuration)
}

// GoString also keeps the credential snapshot out of diagnostic output.
func (p *PresignedDownloadPlan) GoString() string { return p.String() }

type PresignedDownload struct {
	URL                   string
	RequestedDuration     time.Duration
	EffectiveDuration     time.Duration
	SignedAt              time.Time
	ExpiresAt             time.Time
	KnownCredentialExpiry time.Time
	SessionExpiryUnknown  bool
}

// ParsePresignedDuration accepts whole minutes, hours, or days within the
// supported lifetime of an S3 presigned download link.
func ParsePresignedDuration(input string) (time.Duration, error) {
	if len(input) < 2 {
		return 0, fmt.Errorf("enter a duration such as 30m, 2h, or 3d")
	}
	unit := input[len(input)-1]
	var multiplier time.Duration
	switch unit {
	case 'm':
		multiplier = time.Minute
	case 'h':
		multiplier = time.Hour
	case 'd':
		multiplier = 24 * time.Hour
	default:
		return 0, fmt.Errorf("duration unit must be m, h, or d")
	}
	for _, digit := range input[:len(input)-1] {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("duration must be a positive whole number followed by m, h, or d")
		}
	}
	amount, err := strconv.ParseUint(input[:len(input)-1], 10, 64)
	if err != nil || amount == 0 || amount > uint64(MaxPresignedDuration/multiplier) {
		return 0, fmt.Errorf("duration must be between 1m and 7d")
	}
	return time.Duration(amount) * multiplier, nil
}

func validatePresignedDuration(duration time.Duration) error {
	if duration < time.Minute || duration > MaxPresignedDuration || duration%time.Minute != 0 {
		return fmt.Errorf("link duration must be a whole number of minutes between 1m and 7d")
	}
	return nil
}

func (c *Client) PreparePresignedDownload(ctx context.Context, bucket, key, region string, duration time.Duration) (*PresignedDownloadPlan, error) {
	if bucket == "" || key == "" || region == "" {
		return nil, fmt.Errorf("bucket, file key, and bucket region are required")
	}
	if err := validatePresignedDuration(duration); err != nil {
		return nil, err
	}
	if c == nil || c.PresignS3 == nil || c.Credentials == nil {
		return nil, fmt.Errorf("download link signing is unavailable")
	}
	credentials, err := c.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load signing credentials: %w", err)
	}
	if !credentials.HasKeys() {
		return nil, fmt.Errorf("signing credentials have no access key")
	}
	plan := &PresignedDownloadPlan{
		Bucket:               bucket,
		Key:                  key,
		Region:               region,
		RequestedDuration:    duration,
		AvailableDuration:    duration,
		SessionExpiryUnknown: credentials.SessionToken != "" && !credentials.CanExpire,
		credentials:          credentials,
	}
	if credentials.CanExpire {
		plan.KnownCredentialExpiry = credentials.Expires.UTC()
		available := availableCredentialLifetime(credentials.Expires, time.Now())
		if available < time.Minute {
			return nil, ErrPresignedCredentialsExpiring
		}
		if available < duration {
			plan.AvailableDuration = available
			plan.NeedsShorterDuration = true
		}
	}
	return plan, nil
}

func (c *Client) GeneratePresignedDownload(ctx context.Context, plan *PresignedDownloadPlan, acceptShorter bool) (PresignedDownload, error) {
	if c == nil || c.PresignS3 == nil || plan == nil || !plan.credentials.HasKeys() || plan.Bucket == "" || plan.Key == "" || plan.Region == "" {
		return PresignedDownload{}, fmt.Errorf("prepared download link is unavailable")
	}
	if err := validatePresignedDuration(plan.RequestedDuration); err != nil {
		return PresignedDownload{}, err
	}
	effective := plan.RequestedDuration
	if plan.credentials.CanExpire {
		available := availableCredentialLifetime(plan.credentials.Expires, time.Now())
		if available < time.Minute {
			return PresignedDownload{}, ErrPresignedCredentialsExpiring
		}
		if available < effective {
			if !acceptShorter {
				return PresignedDownload{}, ErrPresignedDurationNeedsConfirmation
			}
			effective = available
		}
	}
	snapshot := plan.credentials
	presigned, err := c.PresignS3.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: awsdk.String(plan.Bucket),
		Key:    awsdk.String(plan.Key),
	}, s3.WithPresignExpires(effective), s3.WithPresignClientFromClientOptions(func(options *s3.Options) {
		options.Region = plan.Region
		options.Credentials = awsdk.CredentialsProviderFunc(func(context.Context) (awsdk.Credentials, error) {
			return snapshot, nil
		})
	}))
	if err != nil {
		return PresignedDownload{}, fmt.Errorf("could not sign download link: %w", err)
	}
	if presigned == nil {
		return PresignedDownload{}, fmt.Errorf("signer returned no download link")
	}
	signedAt, expiresAt, err := presignedTimes(presigned.URL, effective)
	if err != nil {
		return PresignedDownload{}, err
	}
	if snapshot.CanExpire && expiresAt.After(snapshot.Expires) {
		return PresignedDownload{}, ErrPresignedCredentialsExpiring
	}
	return PresignedDownload{
		URL:                   presigned.URL,
		RequestedDuration:     plan.RequestedDuration,
		EffectiveDuration:     effective,
		SignedAt:              signedAt,
		ExpiresAt:             expiresAt,
		KnownCredentialExpiry: plan.KnownCredentialExpiry,
		SessionExpiryUnknown:  plan.SessionExpiryUnknown,
	}, nil
}

// Reserve one second for the signer clock and round down to whole seconds.
func availableCredentialLifetime(expiry, now time.Time) time.Duration {
	seconds := int64(expiry.Sub(now)/time.Second) - 1
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func presignedTimes(rawURL string, duration time.Duration) (time.Time, time.Time, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("signer returned an invalid download link: %w", err)
	}
	query := parsed.Query()
	signedAt, err := time.Parse("20060102T150405Z", query.Get("X-Amz-Date"))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("signer returned a link without a valid signing time: %w", err)
	}
	seconds, err := strconv.ParseInt(query.Get("X-Amz-Expires"), 10, 64)
	if err != nil || seconds <= 0 || seconds != int64(duration/time.Second) {
		return time.Time{}, time.Time{}, fmt.Errorf("signer returned a link with an unexpected expiration")
	}
	return signedAt, signedAt.Add(duration), nil
}
