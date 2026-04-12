# Access Management Design

Per-bucket permission levels for s3m-managed IAM users, manageable from both user detail and bucket detail views.

## Permission Levels

Three levels, mapped to IAM S3 actions:

| Level | S3 Actions |
|-------|-----------|
| `read` | GetObject, ListBucket |
| `read-write` | GetObject, PutObject, ListBucket, AbortMultipartUpload, ListMultipartUploadParts, ListBucketMultipartUploads |
| `read-write-delete` | All of read-write + DeleteObject |

Multi-part upload actions are included in write levels so large file uploads work and failed uploads can be cleaned up.

## IAM Policy Structure

Single inline policy per user (`s3m-bucket-access`) with one Statement per bucket. The `Sid` encodes the bucket name, the `Action` array encodes the permission level.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "s3m-my-bucket",
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:ListBucket"],
      "Resource": ["arn:aws:s3:::my-bucket", "arn:aws:s3:::my-bucket/*"]
    },
    {
      "Sid": "s3m-other-bucket",
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:ListBucket", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts", "s3:ListBucketMultipartUploads"],
      "Resource": ["arn:aws:s3:::other-bucket", "arn:aws:s3:::other-bucket/*"]
    }
  ]
}
```

**Reverse mapping** (reading permissions back): Check for DeleteObject → `read-write-delete`, check for PutObject → `read-write`, otherwise → `read`.

**Backward compatibility**: Existing users have a single statement with all actions on all buckets. The parser handles both the new per-bucket-statement format and the legacy grouped format, classifying legacy entries as `read-write-delete`.

## Data Model Changes

**File: `internal/model/types.go`**

Add:
```go
type PermissionLevel string

const (
    PermRead            PermissionLevel = "read"
    PermReadWrite       PermissionLevel = "read-write"
    PermReadWriteDelete PermissionLevel = "read-write-delete"
)

type BucketAccess struct {
    Bucket     string
    Permission PermissionLevel
}

type UserPermission struct {
    Username   string
    Permission PermissionLevel
}
```

Replace `User.Buckets []string` with `User.BucketAccess []BucketAccess`. The old field is never populated by `ListManagedUsers` and unused elsewhere.

## AWS Layer Changes

**File: `internal/aws/client.go`**

Add `GetUserPolicy` to the `IAMAPI` interface:
```go
GetUserPolicy(ctx context.Context, params *iam.GetUserPolicyInput, optFns ...func(*iam.Options)) (*iam.GetUserPolicyOutput, error)
```

**File: `internal/aws/iam.go`**

New functions:
- `actionsForPermission(level) []string` — maps permission level to IAM actions
- `permissionFromActions(actions []string) PermissionLevel` — reverse maps actions to permission level
- `buildBucketPolicyWithPermissions(accesses []BucketAccess) map[string]interface{}` — replaces `buildBucketPolicy`, generates one statement per bucket
- `GetUserBucketAccess(ctx, username) ([]BucketAccess, error)` — calls GetUserPolicy, URL-decodes, parses JSON, extracts per-bucket permissions. Handles legacy single-statement format.
- `SetUserBucketAccess(ctx, username, []BucketAccess) error` — rebuilds and puts the policy. If empty, deletes the policy.
- `ListBucketUsers(ctx, bucketName) ([]UserPermission, error)` — iterates managed users, fetches each policy concurrently (goroutines + WaitGroup), filters for the given bucket.

Modified functions:
- `CreateManagedUser` — accepts `[]BucketAccess` instead of `[]string`, calls `buildBucketPolicyWithPermissions`

## TUI: User Detail View

**File: `internal/tui/users.go`**

New screen accessible via `enter` from user list. Loads bucket access lazily (only when entering detail, not during list load).

New modes:
```
usersDetail              — viewing user's bucket access list
usersDetailPickBucket    — selecting a bucket to add (picker from loaded bucket list, filtered to exclude already-assigned)
usersDetailPickPerm      — choosing permission level (1/2/3) for new bucket
usersDetailConfirmRemove — confirm removing bucket access (y/N)
```

New fields on `usersModel`:
- `detailUser string` — username being viewed
- `detailAccess []model.BucketAccess` — loaded bucket access
- `detailCursor int` — cursor in access list
- `detailLoading bool`
- `detailMessage string`
- `availableBuckets []bucketItem` — for the picker (loaded from app's bucket list)
- `pickerCursor int` — cursor in bucket picker
- `pendingBucket string` — bucket selected in picker, awaiting permission choice

Layout:
```
dashboard > users > s3m-alice
s3m-alice
─────────────────────────────
  Created:  2024-01-15
  Keys:     1

  BUCKET                        PERMISSION
  media-assets                  read-write
> client-uploads                read
  staging-data                  read-write-delete

 [a] Add bucket  [d] Remove  [enter] Cycle permission  [r] Rotate key  [esc] Back
```

Key bindings:
- `enter` — cycle permission: read → read-write → read-write-delete → read. Calls `SetUserBucketAccess`.
- `a` — add bucket: shows picker of buckets not already assigned, then permission selector (1/2/3)
- `d` — remove selected bucket access (y/N confirmation)
- `r` — rotate access key (existing)
- `esc` — back to user list

## TUI: Bucket Detail — Integrated User Access

**File: `internal/tui/buckets.go`**

Users section is shown inline in the existing bucket detail view, between the bucket access toggle and the prefixes section. `ListBucketUsers` is called concurrently with `loadPrefixes` when entering bucket detail (both in a `tea.Batch`).

New modes:
```
bucketDetailPickUser    — selecting a user to add (picker from managed users, filtered)
bucketDetailPickPerm    — choosing permission level for new user
bucketDetailConfirmRemoveUser — confirm removing user access (y/N)
```

New fields on `bucketsModel`:
- `bucketUsers []bucketUserItem` — users with access to current bucket
- `availableUsers []userItem` — for the picker
- `userPickerCursor int`
- `pendingUser string`

Layout (detail cursor spans all sections):
```
dashboard > buckets > media-assets
media-assets
─────────────────────────────
  Region:   us-east-1
  Objects:  1,247
  Size:     3.2 GB
  Created:  2024-01-15

  Bucket Access: 🔒 private

  USER ACCESS (3)                PERMISSION
> s3m-alice                      read-write
  s3m-bob                        read
  s3m-deploy-svc                 read-write-delete

  PREFIXES
  🔒 images/
  🌐 public/
  🔒 uploads/

 [a] Add user  [d] Remove  [enter] Edit permission  [p] Add prefix  [esc] Back
```

Cursor navigation: row 0 = bucket access toggle, rows 1..N = user rows, rows N+1..M = prefix rows.

Context-sensitive help bar changes based on cursor section:
- **Bucket access row**: `[enter] Toggle public/private`
- **User row**: `[a] Add user  [d] Remove  [enter] Cycle permission`
- **Prefix row**: `[enter] Toggle access  [p] Add prefix  [d] Delete  [→] Browse`

## TUI: User Creation Flow

Modify the existing creation flow to add a permission step:

1. Enter username (existing)
2. Enter comma-separated bucket names (existing text input, unchanged)
3. Pick permission level (1/2/3) — applied to all buckets. Individual permissions can be adjusted from the detail view after creation.

New mode: `usersCreatePerm` (permission selector after entering buckets).

## CLI Changes

**File: `cmd/user.go`**

- `user create` — add `--permission` flag (default: `read-write-delete` for backward compatibility)
- Existing `--buckets` flag unchanged

## App-Level Changes

**File: `internal/tui/app.go`**

- Add new text-input modes to `isTextInputActive()` (bucket picker and user picker don't use text inputs, so only confirmation modes need adding)
- Route `screenUserDetail` to user detail view (currently defined but unused)
- Pass bucket list to `usersModel` so the add-bucket picker has data

## Message Types

```go
type userAccessLoadedMsg struct {
    username string
    access   []model.BucketAccess
}

type bucketUsersLoadedMsg struct {
    bucket string
    users  []model.UserPermission
}
```

## Test Plan

**File: `internal/aws/iam_test.go`**

1. Add `GetUserPolicy` to `mockIAM` with `getUserPolicyOutput` / `getUserPolicyErr` fields
2. Test `actionsForPermission` — each level maps to expected actions
3. Test `permissionFromActions` — reverse mapping including edge cases
4. Test `GetUserBucketAccess` — mock policy with multiple statements, verify parsing
5. Test `GetUserBucketAccess` with legacy format — single statement with all actions on multiple buckets → `read-write-delete` for each
6. Test `SetUserBucketAccess` — verify policy document structure passed to PutUserPolicy
7. Test `SetUserBucketAccess` with empty list — verify DeleteUserPolicy is called
8. Test `buildBucketPolicyWithPermissions` — mixed permission levels produce correct statements
9. Test `ListBucketUsers` — multiple users, verify filtering for specific bucket
10. Update `TestCreateManagedUser` — pass `[]BucketAccess` instead of `[]string`

## Known Constraints

- **IAM inline policy size limit**: 2,048 characters. Supports ~10-12 buckets per user with per-bucket statements. Sufficient for this tool's use case.
- **ListBucketUsers performance**: Requires fetching each managed user's policy. Mitigated with concurrent goroutines (matching existing ListBuckets pattern).
- **IAM eventual consistency**: After PutUserPolicy, GetUserPolicy may return stale data. Update local state optimistically; re-read on explicit refresh.
- **Policy document URL encoding**: IAM returns URL-encoded JSON from GetUserPolicy. Must call `url.QueryUnescape` before parsing.
