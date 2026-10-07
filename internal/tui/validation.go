package tui

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

var bucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`)

func validateBucketName(name string) error {
	if len(name) < 3 || len(name) > 63 || !bucketNamePattern.MatchString(name) || strings.Contains(name, "..") || net.ParseIP(name) != nil {
		return fmt.Errorf("use 3-63 lowercase letters, numbers, dots, or hyphens; start and end with a letter or number")
	}
	for _, prefix := range []string{"xn--", "sthree-", "amzn-s3-demo-"} {
		if strings.HasPrefix(name, prefix) {
			return fmt.Errorf("bucket name uses a reserved prefix")
		}
	}
	for _, suffix := range []string{"-s3alias", "--ol-s3", ".mrap", "--x-s3", "--table-s3"} {
		if strings.HasSuffix(name, suffix) {
			return fmt.Errorf("bucket name uses a reserved suffix")
		}
	}
	return nil
}
