# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
