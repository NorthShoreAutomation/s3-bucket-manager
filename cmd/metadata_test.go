package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dcorbell/s3m/internal/model"
)

func TestBucketListReportsUnknownWithoutClaimingPublic(t *testing.T) {
	rows := []bucketListEntry{{Bucket: model.Bucket{Name: "unreadable", IsPublic: false}, PublicSettings: "unknown", MetadataError: "access denied"}, {Bucket: model.Bucket{Name: "blocked"}, AccessKnown: true, PublicSettings: "blocked"}}
	var out bytes.Buffer
	if err := writeBucketList(&out, rows, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unknown") || strings.Contains(out.String(), "private") {
		t.Fatalf("false access claim: %s", out.String())
	}
	out.Reset()
	if err := writeBucketList(&out, rows, true); err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded[0]["Name"] != "unreadable" || decoded[0]["IsPublic"] != false || decoded[0]["AccessKnown"] != false || decoded[0]["PublicSettings"] != "unknown" {
		t.Fatalf("fields not preserved/additive: %v", decoded)
	}
}
func TestUserListKeepsPartialResultsAndUnknownKeyCounts(t *testing.T) {
	users := []model.User{{Name: "known-user", KeyCountKnown: false}}
	var out, warnings bytes.Buffer
	if err := writeUserList(&out, &warnings, users, errors.New("some tags unavailable"), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "known-user") || !strings.Contains(out.String(), "unknown") || !strings.Contains(warnings.String(), "incomplete") {
		t.Fatalf("partial data lost: %s / %s", out.String(), warnings.String())
	}
	if err := writeUserList(&out, &warnings, nil, errors.New("listing denied"), false); err == nil {
		t.Fatal("fatal listing error suppressed")
	}
}
