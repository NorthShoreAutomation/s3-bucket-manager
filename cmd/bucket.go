package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/spf13/cobra"
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

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(buckets)
		}

		if len(buckets) == 0 {
			fmt.Println("No buckets found. Create one with: s3m bucket create <name>")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tREGION\tACCESS\tCREATED")
		for _, b := range buckets {
			access := "private"
			if b.IsPublic {
				access = "public"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Name, b.Region, access, b.CreationDate.Format("2006-01-02"))
		}
		return w.Flush()
	},
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

		count, err := client.GetBucketObjectCount(ctx, name)
		if err != nil {
			return fmt.Errorf("Could not check bucket contents: %w", err)
		}

		if count > 0 {
			return fmt.Errorf("Bucket %q has %d objects. Empty it first before deleting.", name, count)
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

		err = client.DeleteBucket(ctx, name)
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
