# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- TUI user detail view: press Enter on a user to see their bucket access list with permission levels
- Add/remove/edit bucket access from the user detail view
- Bucket picker for adding new bucket access to a user (loads available buckets, filters already-assigned)
- Permission selector (1/2/3) for choosing read, read-write, or read-write-delete access
- Cycle permission on existing bucket access by pressing Enter in the detail view
- Confirmation prompt for removing bucket access
- Key rotation available from the user detail view
- Permission-level access management: `read`, `read-write`, `read-write-delete` permission levels for bucket access
- New types: `PermissionLevel`, `BucketAccess`, `UserPermission` in data model
- `GetUserBucketAccess` — parse IAM inline policy to determine per-bucket permissions
- `SetUserBucketAccess` — set or remove per-bucket permission policies
- `ListBucketUsers` — list all managed users with access to a given bucket
- `--permission` flag on `s3m user create` (default: `read-write-delete`)
- Legacy policy format detection for backward compatibility with existing users

### Changed
- `CreateManagedUser` now accepts `[]BucketAccess` with per-bucket permission levels instead of `[]string`
- IAM policies now generate one statement per bucket with permission-scoped actions
- `User.Buckets` field replaced with `User.BucketAccess` in data model
- `GetUserPolicy` added to `IAMAPI` interface

## [0.1.0] - 2026-04-10
### Added
- s3m - Go TUI for S3 bucket, user, and access management (#1)

### Fixed
- Resolved all golangci-lint errors: goimports formatting, errcheck exclusions, revive unused-parameter, ST1005 capitalized error strings, and removed unused code
- Added `ui` as valid conventional commit type in commit-lint workflow

### Changed
- Merged access control into bucket detail view; press Enter on a bucket to manage public/private access for the bucket and individual prefixes
- Removed separate `[a] Access` screen from the dashboard

### Added
- GitHub Actions CI/CD: lint, test, build, and security (govulncheck) on PR/push
- Conventional commit validation workflow for PR titles and commit messages
- Auto-release workflow with semantic version bumping and CHANGELOG updates on push to main
- GoReleaser-based release workflow triggered by tag push (darwin/linux, amd64/arm64)
- Dependabot configuration for Go modules and GitHub Actions
- Build version injection via ldflags (`s3m --version`)
- Interactive TUI dashboard with Bubble Tea
- TUI screens for buckets, users, and access control
- CLI subcommands: `bucket list/create/delete`
- CLI subcommands: `user list/create/delete/rotate-key`
- CLI subcommands: `access show/set`
- AWS credential resolution via `~/.aws/credentials` with `--profile` support
- IAM user management with `s3m:managed=true` tagging
- Bucket-scoped IAM policies for created users
- Prefix-level public/private access control via bucket policies
- One-time credential display with save-to-file option
- `--json` output flag for all CLI commands
- `--yes` flag to skip confirmation prompts
- Plain-language error messages with actionable suggestions
- Makefile with build, install, test, format, and lint targets
