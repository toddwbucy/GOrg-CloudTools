package credentials_test

import (
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/credentials"
)

func TestValidAWSKeyID_ValidPrefixes(t *testing.T) {
	valid := []string{
		"AKIAIOSFODNN7EXAMPLE", // AKIA — long-term
		"ASIAIOSFODNN7EXAMPLE", // ASIA — STS temp
		"AROAIOSFODNN7EXAMPLE", // AROA — role
		"AIDAIOSFODNN7EXAMPLE", // AIDA — IAM user (legacy)
		"AIPAIOSFODNN7EXAMPLE", // AIPA — service role
	}
	for _, id := range valid {
		if !credentials.ValidAWSKeyID(id) {
			t.Errorf("expected valid for %q", id)
		}
	}
}

func TestValidAWSKeyID_InvalidPrefix(t *testing.T) {
	invalid := []string{
		"XXXX0000000000000000",
		"akiaiosfodnn7example", // lowercase
		"",
		"AKIA",                 // too short
		"AKIAIOSFODNN7EXAMPLELONG", // too long
	}
	for _, id := range invalid {
		if credentials.ValidAWSKeyID(id) {
			t.Errorf("expected invalid for %q", id)
		}
	}
}

func TestValidAWSKeyID_InvalidCharsInSuffix(t *testing.T) {
	// Valid prefix but suffix contains lowercase or special chars.
	if credentials.ValidAWSKeyID("AKIA!@#$%^&*()abcdef") {
		t.Error("expected invalid for key with special characters in suffix")
	}
}

func TestContainsXSS_Detected(t *testing.T) {
	patterns := []string{
		"<script>alert(1)</script>",
		"javascript:void(0)",
		"data:text/html,<h1>x</h1>",
		"vbscript:msgbox",
		"<iframe src=x>",
		"onload=evil()",
		"onerror=evil()",
		"<SCRIPT>ALERT(1)</SCRIPT>", // uppercase
	}
	for _, p := range patterns {
		if !credentials.ContainsXSS(p) {
			t.Errorf("expected XSS detected in %q", p)
		}
	}
}

func TestContainsXSS_Clean(t *testing.T) {
	clean := []string{
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AQoDYXdzEJr//some/session/token+with=chars",
		"normal text value",
		"",
	}
	for _, s := range clean {
		if credentials.ContainsXSS(s) {
			t.Errorf("expected clean (no XSS) for %q", s)
		}
	}
}
