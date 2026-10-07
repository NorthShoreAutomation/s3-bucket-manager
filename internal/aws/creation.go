package aws

import "fmt"

// PartialBucketCreationError identifies a created bucket whose setup failed.
type PartialBucketCreationError struct {
	Bucket string
	Err    error
}

func (e *PartialBucketCreationError) Error() string {
	return fmt.Sprintf("bucket %q was created but public access setup failed: %v. Inspect this bucket before retrying", e.Bucket, e.Err)
}
func (e *PartialBucketCreationError) Unwrap() error { return e.Err }
