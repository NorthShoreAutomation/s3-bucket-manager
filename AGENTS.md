# Repository Guidelines

## Project Structure & Module Organization

`s3m` manages Amazon Web Services (AWS) S3 buckets and Identity and Access Management (IAM) users through command-line and terminal interfaces.

- `main.go`: application entry point.
- `cmd/`: Cobra commands, flags, and output handling.
- `internal/aws/`: service clients, bucket operations, and IAM policies.
- `internal/tui/`: Bubble Tea terminal screens and interaction logic.
- `internal/httpcopy/` and `internal/httpresolve/`: streaming transfers and share-link resolution.
- `internal/progress/`, `internal/model/`, and `internal/buildinfo/`: transfer tracking, shared types, and version metadata.
- Tests sit beside source files as `*_test.go`.
- `docs/superpowers/`: design specifications and implementation plans. Build output goes in ignored `bin/`.

## Build, Test, and Development Commands

Use Go 1.26.1 or newer, as specified in `go.mod`.

- `make build`: compile `bin/s3m` with version metadata.
- `make run`: build and launch the terminal interface using configured AWS credentials.
- `bin/s3m --profile work --bucket example`: open a bucket using a named profile.
- `make test`: run all tests with verbose output.
- `make check`: check formatting, run `go vet`, and execute tests.
- `make fmt`: format Go files.
- `golangci-lint run --timeout=5m`: run configured lint checks.
- `go test -race -v ./...`: run the race checks used in continuous integration (CI).

## Coding Style & Naming Conventions

Use `gofmt` tab indentation and `goimports` import grouping. Keep repository imports under `github.com/dcorbell/s3m` together. Follow `.golangci.yml`. Use lowercase package names, PascalCase exported identifiers, and camelCase internal identifiers. Keep service operations in `internal/aws` and terminal interactions in `internal/tui`.

## Testing Guidelines

Use Go's standard `testing` package and `TestBehavior` function names. Reuse service-interface mocks and local HTTP test servers. Keep automated tests independent of live AWS credentials. Add regression tests for bug fixes. Run focused tests with `go test ./internal/aws -run TestName`, then run the full suite. No numeric coverage threshold is configured.

## Commit & Pull Request Guidelines

Use Conventional Commits for commits and pull request titles, such as `feat(tui): add bulk selection` or `docs: clarify setup`. CI validates both. Describe behavior changes and verification. Link relevant issues and include screenshots for terminal layout changes. Update `CHANGELOG.md` for release-worthy changes. Ensure CI lint, race tests, build, and vulnerability checks pass.

## Security & Configuration

Use AWS profiles or environment variables for credentials. Never commit keys, `.env` files, or credential exports. Use disposable buckets for manual checks that create, delete, or change public access.
