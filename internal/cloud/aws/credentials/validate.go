package credentials

import "strings"

// ValidAWSKeyID reports whether id matches the format AWS uses for access key IDs:
// a known 4-character prefix followed by exactly 16 uppercase alphanumeric characters.
//
// Accepted prefixes:
//
//	AKIA — long-term IAM user key
//	ASIA — STS temporary key (session token required)
//	AROA — role key
//	AIDA — IAM user key (legacy)
//	AIPA — service role key
func ValidAWSKeyID(id string) bool {
	if len(id) != 20 {
		return false
	}
	switch id[:4] {
	case "AKIA", "ASIA", "AROA", "AIDA", "AIPA":
	default:
		return false
	}
	for _, c := range id[4:] {
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// xssPatterns is the set of injection signatures rejected in credential fields.
var xssPatterns = []string{
	"<script", "javascript:", "data:", "vbscript:", "<iframe", "onload=", "onerror=",
}

// ContainsXSS reports whether s contains any known XSS injection pattern.
// Comparison is case-insensitive so <SCRIPT matches the same as <script.
func ContainsXSS(s string) bool {
	lower := strings.ToLower(s)
	for _, p := range xssPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
