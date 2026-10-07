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

- Go 1.26.1+
- The module selects the patched Go 1.26.8 build toolchain automatically. Keep automatic toolchain selection enabled.
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

- `b` opens Buckets. `u` opens Managed users. `r` refreshes the current view.
- `/` activates a filter. Each character immediately narrows names, without pressing Enter.
- Filtering ignores case and matches any part of a name. It searches all loaded buckets or files and folders in the current folder, across listing pages.
- `Enter` keeps filtered results and returns focus to the list. `Esc` clears the filter and restores all entries.
- `Enter` or Right opens a bucket or folder. Left or `Esc` returns to the parent.
- `Tab` switches between Files, Access, and Details. Access changes require a review and explicit confirmation.
- In Files: Space selects, `a` selects displayed results or clears selection, `n` creates a folder, and `d` reviews deletion.
- `p` uploads from disk, `U` uploads from a URL, and `g` downloads to a chosen path. Existing destinations require explicit overwrite consent.
- `s` opens Share for the focused file. `i` shows its full path. `m` or `?` shows available actions.
- In Managed users: `c` creates a user, Enter reviews access, and `K` manages access keys. `r` never creates a key.
- `q` quits. During an operation, choose Stay or Cancel and quit. Cancellation waits for a result.

**Temporary download links:**

Select a file, press `s`, choose 1 hour, 24 hours, 7 days, or Custom, then review and generate.
Custom durations accept whole minutes, hours, or days, such as `30m`, `2h`, or `3d`, within 1 minute to 7 days.
The app copies the generated link. If copying fails, press `c` to retry the same link or `s` to show it.
Anyone with the link can download while access remains valid.

Temporary credentials can expire before the selected duration. The app asks before using a known shorter session lifetime.
The displayed expiration is an upper limit. Policies, revoked credentials, or session expiry can end access sooner.
Signing does not change public access settings. Existing public grants remain active until you change them separately.

**Credentials and access keys:**

Secrets start masked. Reveal, copy, or save them before leaving the one-time credential screen.
Saving creates a new, key-specific file with owner-only permissions and refuses to replace an existing file.
Creating a replacement key leaves the previous key active. Update dependent applications before explicitly deactivating it.
Delete an inactive key only after verifying its replacement. IAM allows two keys per user.

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
- Shows region and known or unknown public block settings, without claiming verified public reachability
- Shows dated daily object counts and Standard storage size when CloudWatch samples are available

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
