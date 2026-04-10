# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
