package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	awsClient "github.com/dcorbell/s3m/internal/aws"
	"github.com/dcorbell/s3m/internal/model"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage IAM users for S3 access",
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List s3m-managed IAM users",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := awsClient.NewClient(ctx, profile, region)
		if err != nil {
			return fmt.Errorf("Could not connect to AWS. Check your credentials.\n  Detail: %w", err)
		}

		users, err := client.ListManagedUsers(ctx)
		if err != nil {
			return err
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(users)
		}

		if len(users) == 0 {
			fmt.Println("No s3m-managed users found. Create one with: s3m user create <username> --buckets <bucket1,bucket2>")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "USERNAME\tKEYS\tCREATED")
		for _, u := range users {
			fmt.Fprintf(w, "%s\t%d\t%s\n", u.Name, u.KeyCount, u.CreateDate.Format("2006-01-02"))
		}
		return w.Flush()
	},
}

var (
	userBuckets    string
	userPermission string
	deleteUserYes  bool
)

var userCreateCmd = &cobra.Command{
	Use:   "create <username>",
	Short: "Create an IAM user with S3 bucket access",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		if userBuckets == "" {
			return fmt.Errorf("Specify which buckets this user can access with --buckets <bucket1,bucket2>")
		}

		// Validate permission level
		perm := model.PermissionLevel(userPermission)
		switch perm {
		case model.PermRead, model.PermReadWrite, model.PermReadWriteDelete:
			// valid
		default:
			return fmt.Errorf("Invalid permission level %q. Must be one of: read, read-write, read-write-delete", userPermission)
		}

		buckets := strings.Split(userBuckets, ",")
		accesses := make([]model.BucketAccess, 0, len(buckets))
		for _, b := range buckets {
			accesses = append(accesses, model.BucketAccess{
				Bucket:     b,
				Permission: perm,
			})
		}

		ctx := context.Background()
		client, err := awsClient.NewClient(ctx, profile, region)
		if err != nil {
			return fmt.Errorf("Could not connect to AWS. Check your credentials.\n  Detail: %w", err)
		}

		key, err := client.CreateManagedUser(ctx, username, accesses)
		if err != nil {
			return err
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(key)
		}

		fmt.Println("User created successfully!")
		fmt.Println()
		fmt.Printf("  Username:       %s\n", key.UserName)
		fmt.Printf("  Access Key ID:  %s\n", key.AccessKeyID)
		fmt.Printf("  Secret Key:     %s\n", key.SecretAccessKey)
		fmt.Printf("  Buckets:        %s\n", strings.Join(buckets, ", "))
		fmt.Printf("  Permission:     %s\n", perm)
		fmt.Println()
		fmt.Println("  WARNING: This is the only time the secret key will be shown.")
		fmt.Println()

		fmt.Print("Save credentials to file? [y/N]: ")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm == "y" || confirm == "Y" {
			return saveCredentials(key.UserName, key.AccessKeyID, key.SecretAccessKey)
		}

		return nil
	},
}

func saveCredentials(username, keyID, secret string) error {
	filename := fmt.Sprintf("%s-credentials.json", username)
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, filename)

	data := map[string]string{
		"username":          username,
		"access_key_id":     keyID,
		"secret_access_key": secret,
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	err = os.WriteFile(path, jsonData, 0600)
	if err != nil {
		return fmt.Errorf("Could not save credentials: %w", err)
	}
	fmt.Printf("Credentials saved to %s\n", path)
	return nil
}

var userDeleteCmd = &cobra.Command{
	Use:   "delete <username>",
	Short: "Delete an s3m-managed IAM user",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		ctx := context.Background()
		client, err := awsClient.NewClient(ctx, profile, region)
		if err != nil {
			return fmt.Errorf("Could not connect to AWS. Check your credentials.\n  Detail: %w", err)
		}

		if !deleteUserYes {
			fmt.Printf("Delete user %q? Their access keys and policies will be removed. [y/N]: ", username)
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		err = client.DeleteManagedUser(ctx, username)
		if err != nil {
			return err
		}

		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"username": username, "status": "deleted"})
		}
		fmt.Printf("Deleted user %q and all their access keys\n", username)
		return nil
	},
}

var userRotateKeyCmd = &cobra.Command{
	Use:   "rotate-key <username>",
	Short: "Generate a new access key for a user",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		ctx := context.Background()
		client, err := awsClient.NewClient(ctx, profile, region)
		if err != nil {
			return fmt.Errorf("Could not connect to AWS. Check your credentials.\n  Detail: %w", err)
		}

		key, err := client.RotateAccessKey(ctx, username)
		if err != nil {
			return err
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(key)
		}

		fmt.Println("New access key created!")
		fmt.Println()
		fmt.Printf("  Access Key ID:  %s\n", key.AccessKeyID)
		fmt.Printf("  Secret Key:     %s\n", key.SecretAccessKey)
		fmt.Println()
		fmt.Println("  WARNING: This is the only time the secret key will be shown.")
		return nil
	},
}

func init() {
	userCreateCmd.Flags().StringVar(&userBuckets, "buckets", "", "Comma-separated list of bucket names to grant access to (required)")
	userCreateCmd.MarkFlagRequired("buckets")
	userCreateCmd.Flags().StringVar(&userPermission, "permission", "read-write-delete", "Permission level: read, read-write, or read-write-delete")
	userDeleteCmd.Flags().BoolVar(&deleteUserYes, "yes", false, "Skip confirmation prompt")

	userCmd.AddCommand(userListCmd)
	userCmd.AddCommand(userCreateCmd)
	userCmd.AddCommand(userDeleteCmd)
	userCmd.AddCommand(userRotateKeyCmd)
	rootCmd.AddCommand(userCmd)
}
