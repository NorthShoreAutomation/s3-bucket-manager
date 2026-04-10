# s3m - S3 Bucket Manager TUI

## Context

Managing S3 buckets, IAM users, and access permissions through the AWS console or raw CLI is tedious and exposes unnecessary complexity. The team needs a straightforward tool to create buckets, manage who can access them, hand out credentials, and control public/private access at the prefix level. A previous Python iteration of this project got over-complicated; this is a fresh start in Go prioritizing simplicity.

## Overview

`s3m` is a Go TUI application for managing AWS S3 buckets, IAM users, credentials, and public/private access. It uses `~/.aws` credentials by default, provides a hybrid dashboard+drill-down TUI via Bubble Tea, and exposes CLI subcommands for scripting.

**Target users**: Technical team members who shouldn't need to know AWS internals.

## Dependencies

| Library | Purpose |
|---------|---------|
| `github.com/charmbracelet/bubbletea` | TUI framework (Elm architecture) |
| `github.com/charmbracelet/lipgloss` | Terminal styling |
| `github.com/charmbracelet/bubbles` | Pre-built TUI components (table, textinput, spinner, list) |
| `github.com/spf13/cobra` | CLI subcommand routing |
| `github.com/aws/aws-sdk-go-v2` | AWS SDK (S3, IAM, STS) |
| `github.com/aws/aws-sdk-go-v2/config` | Credential loading |

## Architecture

### Project Structure

```
s3m/
├── main.go                  # Entry point, routes TUI vs CLI
├── cmd/                     # CLI subcommands (cobra)
│   ├── root.go              # Root command, --profile flag, TUI launch
│   ├── bucket.go            # bucket list/create/delete
│   ├── user.go              # user list/create/delete/rotate-key
│   └── access.go            # access show/set
├── internal/
│   ├── aws/                 # AWS service layer
│   │   ├── s3.go            # Bucket + object operations
│   │   ├── iam.go           # IAM user + policy operations
│   │   └── session.go       # Credential loading, region config
│   ├── tui/                 # Bubble Tea models
│   │   ├── app.go           # Root model, screen routing
│   │   ├── dashboard.go     # Main dashboard view
│   │   ├── buckets.go       # Bucket list + CRUD screens
│   │   ├── users.go         # IAM user management screens
│   │   ├── access.go        # Permissions/public-private screens
│   │   ├── credentials.go   # Credential display + save overlay
│   │   └── components/      # Shared TUI components
│   └── model/               # Domain types (Bucket, User, Permission)
├── go.mod
├── go.sum
├── CHANGELOG.md
└── README.md
```

### Two Modes

- **TUI mode**: `s3m` (no args) launches the interactive dashboard
- **CLI mode**: `s3m <command> [args]` for scripting

### AWS Credential Resolution

Standard SDK chain: env vars > `~/.aws/credentials` > instance role. Optional `--profile` flag selects a named profile.

## TUI Screens

### Dashboard (Home)

Summary view showing:
- Current AWS profile + region
- Bucket count with storage summary
- Quick-action keys: `[b]` Buckets, `[u]` Users, `[a]` Access, `[q]` Quit

### Buckets Screen

- Table: name, region, public/private status, object count
- Actions: `[c]` Create, `[d]` Delete, `[Enter]` Drill into bucket
- Drill-in shows prefixes/objects with their access status
- Create flow: name input > region select > confirmation

### Users Screen

- Table: IAM users managed by s3m (tagged `s3m:managed=true`)
- Actions: `[c]` Create, `[d]` Delete, `[k]` Rotate key, `[Enter]` View details
- Create flow: username > select buckets > confirm > display credentials + offer to save

### Access Screen

- Select bucket > see prefixes with public/private indicators
- Toggle prefix or whole bucket between public/private
- Shows before/after when toggling

### Credentials Overlay

- Displays access key ID + secret after creation
- Warning: "This is the only time the secret key will be shown"
- Save to file (JSON or CSV) option
- Copy to clipboard shortcut

### Navigation

- `Esc` goes back one level
- `?` shows help overlay with all keybindings
- Breadcrumb at top: `Dashboard > Buckets > my-bucket`

## CLI Subcommands

```
s3m                                                # Launch TUI
s3m --profile work                                 # TUI with named profile

# Buckets
s3m bucket list
s3m bucket create <name> [--region us-west-2]
s3m bucket delete <name>

# Users
s3m user list
s3m user create <username> --buckets <bucket1,bucket2>
s3m user delete <username>
s3m user rotate-key <username>

# Access
s3m access show <bucket>
s3m access set <bucket> --prefix installers/ --public
s3m access set <bucket> --prefix data/ --private
s3m access set <bucket> --public
```

- Plain text output by default, `--json` for machine-readable
- Destructive commands require `--yes` to skip confirmation (for scripting)

## AWS Operations

### Bucket CRUD

- **Create**: `CreateBucket` + `PutPublicAccessBlock` (all blocked by default). Silently handle `us-east-1` LocationConstraint quirk.
- **List**: `ListBuckets` + `GetBucketLocation` for region.
- **Delete**: List objects first, warn if non-empty, require confirmation. Won't force-delete unless user explicitly confirms "delete all objects first".

### IAM User Management

- All s3m-created users tagged `s3m:managed=true` — TUI only shows its own users
- **Create**: `CreateUser` > tag > `CreateAccessKey` > attach inline policy scoped to selected buckets
- **Policy template**: allows `s3:GetObject`, `s3:PutObject`, `s3:ListBucket`, `s3:DeleteObject` on specified bucket ARNs
- **Delete**: detach policies > delete access keys > delete user
- **Rotate keys**: `CreateAccessKey` > display new > offer to delete old key

### Prefix-Level Public/Private Access

- **Make prefix public**:
  1. Modify bucket's `PublicAccessBlock` to allow public policies (if blocking)
  2. Add bucket policy statement granting `s3:GetObject` to `*` on `arn:aws:s3:::bucket/prefix/*`
  3. Each public prefix gets a `Sid` like `s3m-public-installers`
- **Make prefix private**: Remove that policy statement. If no public prefixes remain, re-enable `PublicAccessBlock`.
- **Whole bucket**: Same approach on `arn:aws:s3:::bucket/*`

## Error Handling

- Plain language always — no AWS jargon in user-facing messages
- Report ALL validation errors at once (not one-at-a-time)
- Missing IAM permissions: tell user which permission they need
- Bucket name taken: suggest alternatives (append account ID suffix)
- All destructive operations require explicit confirmation
- Making something public shows a clear warning about what becomes accessible

## Testing Strategy

- **Unit tests**: AWS service layer with mocked SDK clients (interface-based)
- **TUI tests**: Bubble Tea's `tea.Msg` testing pattern for model updates
- **Integration tests**: Against real AWS (optional, behind build tag)
- **CLI tests**: Cobra command testing with captured output

## Verification

1. Build: `go build -o bin/s3m .`
2. Run TUI: `./bin/s3m` — verify dashboard loads, shows profile/region
3. Create a test bucket via TUI and CLI
4. Create an IAM user scoped to that bucket, verify credentials display
5. Toggle a prefix to public, verify bucket policy is correct via AWS console
6. Toggle it back to private, verify policy statement removed
7. Delete user and bucket, verify cleanup
