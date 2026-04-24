# s3m - S3 Bucket Manager

A straightforward TUI and CLI tool for managing AWS S3 buckets, IAM users, credentials, and public/private access.

## Install

```bash
go install github.com/dcorbell/s3m@latest
```

Or build from source:

```bash
git clone https://github.com/dcorbell/s3m.git
cd s3m
go build -o bin/s3m .
```

## Prerequisites

- Go 1.21+
- AWS credentials configured (`~/.aws/credentials` or environment variables)
- IAM permissions: S3 (full), IAM (CreateUser, DeleteUser, TagUser, etc.), STS (GetCallerIdentity)

## Usage

### TUI Mode

Launch the interactive dashboard:

```bash
s3m                      # Uses default AWS profile
s3m --profile work       # Uses named profile
s3m --region us-west-2   # Override region
s3m --bucket my-bucket   # Open directly inside bucket (skips bucket list;
                         # use when credentials lack s3:ListAllMyBuckets)
```

**Keyboard shortcuts:**
- `b` Buckets, `u` Users, `a` Access control
- `c` Create, `d` Delete, `r` Rotate key
- `Enter` Select/drill in, `Esc` Go back
- In the file browser: `p` upload from disk, `g` download, `U` upload from URL
- `?` Help, `q` Quit

### CLI Mode

```bash
# Buckets
s3m bucket list
s3m bucket create my-bucket --region us-west-2
s3m bucket delete my-bucket --yes

# Users (IAM users with bucket-scoped access)
s3m user list
s3m user create alice --buckets my-bucket,other-bucket
s3m user delete alice --yes
s3m user rotate-key alice

# Access control (prefix-level public/private)
s3m access show my-bucket
s3m access set my-bucket --prefix installers/ --public --yes
s3m access set my-bucket --prefix data/ --private
s3m access set my-bucket --public  # Whole bucket

# HTTP to S3 streaming copy
s3m http-copy 'https://example.com/big.zip' s3://my-bucket/inbox/
s3m http-copy 'https://foo.wetransfer.com/downloads/<id>/<hash>' s3://my-bucket/inbox/
s3m http-copy 'https://example.com/big.zip' s3://my-bucket/exact/key.zip --part-size 256MiB
```

`http-copy` streams the response body straight into S3 via multipart upload —
no local disk buffering. When the URL host matches `wetransfer.com`, the share
link is first resolved to its direct CloudFront download URL.

Part size is auto-computed from `Content-Length` to stay under S3's 10,000-part
cap (files up to ~700 GB need ≥70 MiB parts). Override with `--part-size` if
the source omits `Content-Length`. Memory cost is roughly `partSize × concurrency`
(default concurrency 5), so 128 MiB parts use ~640 MiB RAM.

**Flags:**
- `--profile` AWS profile name
- `--region` AWS region
- `--bucket` Open TUI directly inside the given bucket (skips bucket list)
- `--json` Machine-readable JSON output
- `--yes` Skip confirmation prompts (for scripting)

## How It Works

### Buckets
- Creates buckets with public access blocked by default
- Shows region, public/private status, and object count

### Users
- Creates IAM users tagged `s3m:managed=true`
- Attaches inline policies granting S3 access to specified buckets
- Generates access keys and displays credentials (one-time view)
- Only shows/manages users it created

### Access Control
- Toggle entire prefixes (folders) between public and private
- Public access uses bucket policies with `s3:GetObject` grants to `*`
- Each prefix gets its own policy statement (e.g., `s3m-public-installers`)
- Removing all public prefixes re-enables full public access blocking

## License

MIT
