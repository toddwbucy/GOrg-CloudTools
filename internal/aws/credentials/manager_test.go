package credentials_test

import (
	"context"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	"github.com/toddwbucy/GOrg-CloudTools/internal/aws/credentials"
)

// ── HomeRegion ────────────────────────────────────────────────────────────────

func TestHomeRegion_Com(t *testing.T) {
	region, err := credentials.HomeRegion("com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "us-east-1" {
		t.Errorf("want us-east-1, got %q", region)
	}
}

func TestHomeRegion_Gov(t *testing.T) {
	region, err := credentials.HomeRegion("gov")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "us-gov-west-1" {
		t.Errorf("want us-gov-west-1, got %q", region)
	}
}

func TestHomeRegion_Unknown(t *testing.T) {
	_, err := credentials.HomeRegion("cn")
	if err == nil {
		t.Fatal("expected error for unknown environment")
	}
}

func TestHomeRegion_Empty(t *testing.T) {
	_, err := credentials.HomeRegion("")
	if err == nil {
		t.Fatal("expected error for empty environment")
	}
}

// ── FromSession ───────────────────────────────────────────────────────────────
//
// FromSession calls awsconfig.LoadDefaultConfig with static credentials, which
// builds an aws.Config in memory and makes no network calls. The Validate step
// (STS GetCallerIdentity) is intentionally not called here — that requires live
// AWS and belongs in integration tests.

func TestFromSession_EmptySession_ReturnsError(t *testing.T) {
	_, _, err := credentials.FromSession(context.Background(), &middleware.Session{})
	if err == nil {
		t.Fatal("expected error for empty session")
	}
}

func TestFromSession_MissingSecretKey_ReturnsError(t *testing.T) {
	sess := &middleware.Session{
		AWSAccessKeyID: "AKIAIOSFODNN7EXAMPLE",
		AWSEnvironment: "com",
	}
	_, _, err := credentials.FromSession(context.Background(), sess)
	if err == nil {
		t.Fatal("expected error when secret key is missing")
	}
}

func TestFromSession_MissingAccessKey_ReturnsError(t *testing.T) {
	sess := &middleware.Session{
		AWSSecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		AWSEnvironment:     "com",
	}
	_, _, err := credentials.FromSession(context.Background(), sess)
	if err == nil {
		t.Fatal("expected error when access key is missing")
	}
}

func TestFromSession_UnknownEnvironment_ReturnsError(t *testing.T) {
	sess := &middleware.Session{
		AWSAccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		AWSSecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		AWSEnvironment:     "cn", // not "com" or "gov"
	}
	_, _, err := credentials.FromSession(context.Background(), sess)
	if err == nil {
		t.Fatal("expected error for unknown environment")
	}
}

func TestFromSession_ComEnvironment_BuildsConfig(t *testing.T) {
	sess := &middleware.Session{
		AWSAccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		AWSSecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		AWSEnvironment:     "com",
	}
	cfg, region, err := credentials.FromSession(context.Background(), sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "us-east-1" {
		t.Errorf("home region: want us-east-1, got %q", region)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("cfg.Region: want us-east-1, got %q", cfg.Region)
	}
}

func TestFromSession_GovEnvironment_BuildsConfig(t *testing.T) {
	sess := &middleware.Session{
		AWSAccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		AWSSecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		AWSEnvironment:     "gov",
	}
	cfg, region, err := credentials.FromSession(context.Background(), sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "us-gov-west-1" {
		t.Errorf("home region: want us-gov-west-1, got %q", region)
	}
	if cfg.Region != "us-gov-west-1" {
		t.Errorf("cfg.Region: want us-gov-west-1, got %q", cfg.Region)
	}
}

func TestFromSession_SessionToken_Included(t *testing.T) {
	// Verify that a non-empty session token doesn't cause an error at config
	// build time. Token validity is only checked when AWS is actually called.
	sess := &middleware.Session{
		AWSAccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		AWSSecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		AWSSessionToken:    "AQoDYXdzEJr//some/session/token",
		AWSEnvironment:     "com",
	}
	_, _, err := credentials.FromSession(context.Background(), sess)
	if err != nil {
		t.Fatalf("unexpected error with session token: %v", err)
	}
}

// Integration tests (require live AWS credentials) are in
// internal/aws/credentials/integration_test.go and are skipped by default.
// Run with: go test -tags integration ./internal/aws/credentials/...
