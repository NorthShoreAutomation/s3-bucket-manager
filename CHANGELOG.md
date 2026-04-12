# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]
### Added
- Download file from S3 via `[g]` (get) key in file browser — saves selected file to current working directory
- Upload file to S3 via `[p]` (put) key in file browser — opens a local file picker that mirrors the S3 browser's look and feel for filesystem navigation and file selection

### Changed
- Renamed "add prefix" keybinding from `[p]` to `[c]` in prefix list view for consistency with bucket list's `[c] Create`

## [0.4.0] - 2026-04-12
### Added
- add [c] keybinding to copy public URL to clipboard (#5)
### Added
- Press `c` in file browser to copy the public URL of the selected file or folder to the clipboard

## [0.3.0] - 2026-04-12
### Added
- add page up/down navigation to bucket list and file browser (#6)

### Added
- Page Up / Page Down keyboard navigation in bucket list and file browser views

## [0.2.0] - 2026-04-12
### Added
- add permission-level access management (#4)

### Added
- Per-bucket permission levels: `read`, `read-write`, `read-write-delete` for managed IAM users
- Multi-part upload actions (AbortMultipartUpload, ListMultipartUploadParts, ListBucketMultipartUploads) included in write permission levels
- TUI user detail view: press Enter on a user to see their bucket access with permission levels
- TUI bucket detail view: USER ACCESS section showing assigned users and their permissions inline
- Add, remove, and cycle permissions from both user detail and bucket detail views
- Bucket picker and user picker for granting access (filters out already-assigned items)
- Permission selector (1/2/3) during user creation and when adding access
- Context-sensitive help bar in bucket detail view (adapts to cursor section)
- `--permission` flag on `s3m user create` CLI command (default: `read-write-delete`)
- Backward compatibility: existing users with legacy policies detected as `read-write-delete`

### Changed
- IAM policies now generate one statement per bucket with permission-scoped actions (was single statement for all buckets)
- User creation flow includes permission level selection step
- `CreateManagedUser` accepts per-bucket permission levels instead of plain bucket names

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
