package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	awsClient "github.com/dcorbell/s3m/internal/aws"
)

var accessCmd = &cobra.Command{
	Use:   "access",
	Short: "Manage public/private access for buckets and prefixes",
}

var accessShowCmd = &cobra.Command{
	Use:   "show <bucket>",
	Short: "Show public/private status of bucket prefixes",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket := args[0]
		ctx := context.Background()
		client, err := awsClient.NewClient(ctx, profile, region)
		if err != nil {
			return fmt.Errorf("Could not connect to AWS. Check your credentials.\n  Detail: %w", err)
		}

		bucketRegion, err := client.GetBucketRegion(ctx, bucket)
		if err != nil {
			return err
		}

		prefixes, err := client.ListPrefixes(ctx, bucket, bucketRegion)
		if err != nil {
			return err
		}

		if len(prefixes) == 0 {
			fmt.Printf("Bucket %q has no prefixes (folders).\n", bucket)
			return nil
		}

		accesses, err := client.GetPrefixAccessStatus(ctx, bucket, bucketRegion, prefixes)
		if err != nil {
			return err
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(accesses)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "PREFIX\tACCESS\n")
		for _, a := range accesses {
			access := "private"
			if a.IsPublic {
				access = "PUBLIC"
			}
			fmt.Fprintf(w, "%s\t%s\n", a.Prefix, access)
		}
		return w.Flush()
	},
}

var (
	accessPrefix  string
	accessPublic  bool
	accessPrivate bool
	accessYes     bool
)

var accessSetCmd = &cobra.Command{
	Use:   "set <bucket>",
	Short: "Set public/private access for a bucket or prefix",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket := args[0]

		if !accessPublic && !accessPrivate {
			return fmt.Errorf("Specify --public or --private")
		}
		if accessPublic && accessPrivate {
			return fmt.Errorf("Cannot set both --public and --private")
		}

		target := bucket
		if accessPrefix != "" {
			target = fmt.Sprintf("%s/%s", bucket, accessPrefix)
		} else {
			accessPrefix = "" // whole bucket
		}

		ctx := context.Background()
		client, err := awsClient.NewClient(ctx, profile, region)
		if err != nil {
			return fmt.Errorf("Could not connect to AWS. Check your credentials.\n  Detail: %w", err)
		}
		bucketRegion, err := client.GetBucketRegion(ctx, bucket)
		if err != nil {
			return err
		}

		if accessPublic && !accessYes {
			fmt.Printf("Make %s PUBLIC? Anyone on the internet will be able to read its contents. [y/N]: ", target)
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		prefix := accessPrefix
		if prefix == "" {
			prefix = "" // whole bucket uses empty prefix handled as "*"
		}

		if accessPublic {
			// For whole bucket, use empty prefix which results in bucket/*
			err = client.SetPrefixPublic(ctx, bucket, prefix, bucketRegion)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(map[string]string{"target": target, "access": "public"})
			}
			fmt.Printf("Set %s to PUBLIC\n", target)
		} else {
			err = client.SetPrefixPrivate(ctx, bucket, prefix, bucketRegion)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(os.Stdout).Encode(map[string]string{"target": target, "access": "private"})
			}
			fmt.Printf("Set %s to PRIVATE\n", target)
		}

		return nil
	},
}

func init() {
	accessSetCmd.Flags().StringVar(&accessPrefix, "prefix", "", "Prefix (folder) to set access for (omit for whole bucket)")
	accessSetCmd.Flags().BoolVar(&accessPublic, "public", false, "Make public")
	accessSetCmd.Flags().BoolVar(&accessPrivate, "private", false, "Make private")
	accessSetCmd.Flags().BoolVar(&accessYes, "yes", false, "Skip confirmation prompt")

	accessCmd.AddCommand(accessShowCmd)
	accessCmd.AddCommand(accessSetCmd)
	rootCmd.AddCommand(accessCmd)
}
