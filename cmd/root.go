package cmd

import (
	"github.com/spf13/cobra"
)

var (
	profile string
	region  string
	jsonOut bool
)

var rootCmd = &cobra.Command{
	Use:   "s3m",
	Short: "Manage S3 buckets, users, and permissions",
	Long:  "s3m is a TUI and CLI tool for managing AWS S3 buckets, IAM users, credentials, and public/private access.",
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "", "AWS profile name (default: from ~/.aws/credentials)")
	rootCmd.PersistentFlags().StringVar(&region, "region", "", "AWS region (default: from profile or us-east-1)")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "Output in JSON format")
}
