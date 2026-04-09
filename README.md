# S3 Bucket Manager

A Go CLI tool for managing AWS S3 buckets.

## Prerequisites

- Go 1.21+
- AWS account with appropriate IAM permissions
- AWS credentials configured (`~/.aws/credentials` or environment variables)

## Installation

```bash
go install github.com/dcorbell/s3-bucket-manager@latest
```

Or build from source:

```bash
git clone https://github.com/dcorbell/s3-bucket-manager.git
cd s3-bucket-manager
go build -o bin/s3-bucket-manager .
```

## Configuration

The tool uses standard AWS credential resolution:

1. Environment variables (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`)
2. Shared credentials file (`~/.aws/credentials`)
3. IAM role (when running on AWS infrastructure)

## Usage

```bash
s3-bucket-manager [command] [flags]
```

## Project Structure

```
.
├── main.go
├── go.mod
├── go.sum
└── README.md
```

## License

MIT
