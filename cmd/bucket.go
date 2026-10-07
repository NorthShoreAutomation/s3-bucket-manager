package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
)

var bucketCmd = &cobra.Command{
	Use:   "bucket",
	Short: "Manage S3 buckets",
}

var bucketListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all S3 buckets",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := awsClient.NewClient(ctx, profile, region)
		if err != nil {
			return fmt.Errorf("Could not connect to AWS. Check your credentials in ~/.aws/credentials.\n  Detail: %w", err)
		}

		buckets, err := client.ListBuckets(ctx)
		if err != nil {
			return fmt.Errorf("Could not list buckets: %w", err)
		}

		rows := make([]bucketListEntry, 0, len(buckets))
		for _, b := range buckets {
			row := bucketListEntry{Bucket: b, AccessKnown: b.AccessKnown, PublicSettings: "unknown"}
			if b.AccessKnown {
				row.PublicSettings = "blocked"
				if b.IsPublic {
					row.PublicSettings = "not fully blocked"
				}
			} else {
				row.MetadataError = "Public settings could not be read."
			}
			rows = append(rows, row)
		}
		return writeBucketList(cmd.OutOrStdout(), rows, jsonOut)
	},
}

// bucketListEntry preserves the existing JSON fields and adds metadata status.
type bucketListEntry struct {
	model.Bucket
	AccessKnown    bool
	PublicSettings string
	MetadataError  string `json:",omitempty"`
}

func writeBucketList(out io.Writer, rows []bucketListEntry, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "No buckets found. Create one with: s3m bucket create <name>")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tREGION\tPUBLIC SETTINGS\tCREATED")
	for _, b := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Name, b.Region, b.PublicSettings, b.CreationDate.Format("2006-01-02"))
	}
	return w.Flush()
}

var bucketCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new S3 bucket",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		ctx := context.Background()
		client, err := awsClient.NewClient(ctx, profile, region)
		if err != nil {
			return fmt.Errorf("Could not connect to AWS. Check your credentials in ~/.aws/credentials.\n  Detail: %w", err)
		}

		bucketRegion := region
		if bucketRegion == "" {
			bucketRegion = client.Region
		}

		err = client.CreateBucket(ctx, name, bucketRegion)
		if err != nil {
			return fmt.Errorf("Could not create bucket. The name %q may already be taken.\n  Try: %s-%s\n  Detail: %w",
				name, name, client.Account, err)
		}

		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"name":   name,
				"region": bucketRegion,
				"status": "created",
			})
		}
		fmt.Printf("Created bucket %q in %s (private by default)\n", name, bucketRegion)
		return nil
	},
}

var (
	deleteBucketYes bool
)

var bucketDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an S3 bucket",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		ctx := context.Background()
		client, err := awsClient.NewClient(ctx, profile, region)
		if err != nil {
			return fmt.Errorf("Could not connect to AWS. Check your credentials.\n  Detail: %w", err)
		}

		// Look up bucket region to avoid cross-region 301 redirects
		bucketRegion := client.Region
		buckets, _ := client.ListBuckets(ctx)
		for _, b := range buckets {
			if b.Name == name {
				bucketRegion = b.Region
				break
			}
		}

		// Real-time empty check (CloudWatch stats can be stale)
		empty, err := client.IsBucketEmpty(ctx, name, bucketRegion)
		if err != nil {
			return fmt.Errorf("Could not check bucket contents: %w", err)
		}
		if !empty {
			return fmt.Errorf("Bucket %q is not empty. Remove all objects first before deleting.", name)
		}

		if !deleteBucketYes {
			fmt.Printf("Delete bucket %q? This cannot be undone. [y/N]: ", name)
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		err = client.DeleteBucket(ctx, name, bucketRegion)
		if err != nil {
			return err
		}

		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"name":   name,
				"status": "deleted",
			})
		}
		fmt.Printf("Deleted bucket %q\n", name)
		return nil
	},
}

func init() {
	bucketDeleteCmd.Flags().BoolVar(&deleteBucketYes, "yes", false, "Skip confirmation prompt")

	bucketCmd.AddCommand(bucketListCmd)
	bucketCmd.AddCommand(bucketCreateCmd)
	bucketCmd.AddCommand(bucketDeleteCmd)
	rootCmd.AddCommand(bucketCmd)
}
