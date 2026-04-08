package credentials

import "strings"

// ValidAWSKeyID reports whether id matches the format AWS uses for access key IDs:
// a known 4-character prefix followed by exactly 16 uppercase alphanumeric characters.
//
// Accepted prefixes:
//
//	AKIA — long-term IAM user key
//	ASIA — STS temporary key (session token required)
//
// AROA, AIDA, and AIPA are IAM principal IDs, not access key IDs, and are
// intentionally excluded.
func ValidAWSKeyID(id string) bool {
	if len(id) != 20 {
		return false
	}
	switch id[:4] {
	case "AKIA", "ASIA":
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

// xssPatterns is the fixed set of injection signatures checked by
// ContainsKnownXSSPattern. Encoded or obfuscated variants are not covered.
var xssPatterns = []string{
	"<script", "javascript:", "data:", "vbscript:", "<iframe", "onload=", "onerror=",
}

// ContainsKnownXSSPattern reports whether s contains any of the known,
// case-insensitive XSS injection signatures in xssPatterns. This is a
// heuristic defence-in-depth check for credential input fields; it does NOT
// provide full XSS protection. Encoded, obfuscated, or novel payloads will not
// be caught. Proper output encoding at render boundaries is still required.
func ContainsKnownXSSPattern(s string) bool {
	lower := strings.ToLower(s)
	for _, p := range xssPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
