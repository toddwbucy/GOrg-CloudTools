package tui

import (
	"github.com/aws/aws-sdk-go-v2/aws"
)

// CloudEnv holds active credentials and the identity confirmed by STS for one
// cloud environment. A nil entry in the root model's cloudEnvs map means
// no credentials have been loaded for that environment.
type CloudEnv struct {
	Cfg       aws.Config
	AccountID string
	UserARN   string
}

// envKey returns the map key for a given provider+environment pair.
// Example: envKey("aws", "com") == "aws-com"
// This convention anticipates Phase 3 where Azure and GCP envs are added.
func envKey(provider, env string) string { return provider + "-" + env }
