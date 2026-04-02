// White-box tests for internal EC2 helpers. This file is in package ec2
// (not package ec2_test) so it can access unexported functions.
//
// Tests that require a real EC2 endpoint (ListRunning) belong in integration
// tests and are not present here.
package ec2

import (
	"testing"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/aws"
)

// ── normalizePlatform ─────────────────────────────────────────────────────────

func TestNormalizePlatform_Windows(t *testing.T) {
	got := normalizePlatform(ec2types.PlatformValuesWindows)
	if got != "windows" {
		t.Errorf("want windows, got %q", got)
	}
}

func TestNormalizePlatform_Empty_IsLinux(t *testing.T) {
	// EC2 returns an empty Platform field for Linux instances.
	got := normalizePlatform(ec2types.PlatformValues(""))
	if got != "linux" {
		t.Errorf("want linux for empty platform, got %q", got)
	}
}

func TestNormalizePlatform_UnknownValue_IsLinux(t *testing.T) {
	// Any unrecognised value should fall through to "linux".
	got := normalizePlatform(ec2types.PlatformValues("other"))
	if got != "linux" {
		t.Errorf("want linux for unknown platform, got %q", got)
	}
}

// ── nameTag ───────────────────────────────────────────────────────────────────

func TestNameTag_Found(t *testing.T) {
	tags := []ec2types.Tag{
		{Key: aws.String("Env"), Value: aws.String("prod")},
		{Key: aws.String("Name"), Value: aws.String("web-01")},
	}
	got := nameTag(tags)
	if got != "web-01" {
		t.Errorf("want web-01, got %q", got)
	}
}

func TestNameTag_NotPresent(t *testing.T) {
	tags := []ec2types.Tag{
		{Key: aws.String("Env"), Value: aws.String("prod")},
	}
	got := nameTag(tags)
	if got != "" {
		t.Errorf("want empty string, got %q", got)
	}
}

func TestNameTag_EmptySlice(t *testing.T) {
	got := nameTag([]ec2types.Tag{})
	if got != "" {
		t.Errorf("want empty string for empty tags, got %q", got)
	}
}

func TestNameTag_NilSlice(t *testing.T) {
	got := nameTag(nil)
	if got != "" {
		t.Errorf("want empty string for nil tags, got %q", got)
	}
}

func TestNameTag_FirstMatchReturned(t *testing.T) {
	// Tags are unordered in practice, but the function returns the first Name it finds.
	tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String("first")},
		{Key: aws.String("Name"), Value: aws.String("second")},
	}
	got := nameTag(tags)
	if got != "first" {
		t.Errorf("want first, got %q", got)
	}
}
