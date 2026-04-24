package cmd

import (
	"github.com/spf13/cobra"

	"github.com/dcorbell/s3m/internal/buildinfo"
	"github.com/dcorbell/s3m/internal/tui"
)

var (
	profile string
	region  string
	bucket  string
	jsonOut bool
)

var rootCmd = &cobra.Command{
	Use:   "s3m",
	Short: "Manage S3 buckets, users, and permissions",
	Long:  "s3m is a TUI and CLI tool for managing AWS S3 buckets, IAM users, credentials, and public/private access.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return tui.Run(profile, region, bucket)
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.Version = buildinfo.Version
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "", "AWS profile name (default: from ~/.aws/credentials)")
	rootCmd.PersistentFlags().StringVar(&region, "region", "", "AWS region (default: from profile or us-east-1)")
	rootCmd.PersistentFlags().StringVar(&bucket, "bucket", "", "Open directly inside this bucket, skipping the bucket list (for credentials without s3:ListAllMyBuckets)")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "Output in JSON format")
}
