// White-box tests for internal VPC helpers. This file is in package vpc
// (not package vpc_test) so it can access unexported functions.
//
// Tests that require a real EC2/VPC endpoint (Describe) belong in integration
// tests and are not present here.
package vpc

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ── tagsToMap ─────────────────────────────────────────────────────────────────

func TestTagsToMap_Nil_ReturnsNil(t *testing.T) {
	got := tagsToMap(nil)
	if got != nil {
		t.Errorf("want nil for nil input, got %v", got)
	}
}

func TestTagsToMap_Empty_ReturnsNil(t *testing.T) {
	got := tagsToMap([]ec2types.Tag{})
	if got != nil {
		t.Errorf("want nil for empty slice, got %v", got)
	}
}

func TestTagsToMap_SingleTag(t *testing.T) {
	tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String("my-vpc")},
	}
	got := tagsToMap(tags)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if got["Name"] != "my-vpc" {
		t.Errorf("Name: want my-vpc, got %q", got["Name"])
	}
}

func TestTagsToMap_MultipleTags(t *testing.T) {
	tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String("my-vpc")},
		{Key: aws.String("Env"), Value: aws.String("production")},
		{Key: aws.String("Owner"), Value: aws.String("platform")},
	}
	got := tagsToMap(tags)
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d", len(got))
	}
	cases := map[string]string{
		"Name":  "my-vpc",
		"Env":   "production",
		"Owner": "platform",
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("key %q: want %q, got %q", k, want, got[k])
		}
	}
}

func TestTagsToMap_DuplicateKey_LastWins(t *testing.T) {
	// Duplicate keys are unusual in AWS but the map write order means the last
	// value for a given key is retained.
	tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String("first")},
		{Key: aws.String("Name"), Value: aws.String("second")},
	}
	got := tagsToMap(tags)
	if len(got) != 1 {
		t.Fatalf("want 1 entry for duplicate keys, got %d", len(got))
	}
	if got["Name"] != "second" {
		t.Errorf("duplicate key: want second (last), got %q", got["Name"])
	}
}
