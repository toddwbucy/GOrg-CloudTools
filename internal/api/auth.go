package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	gorgaws "github.com/toddwbucy/gorg-aws"
)

type awsCredentialsRequest struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
	Environment     string `json:"environment"` // "com" or "gov"
	Region          string `json:"region,omitempty"`
}

// handleCreateCredentials validates AWS credentials via STS GetCallerIdentity
// and stores them in an encrypted session cookie.
func (s *Server) handleCreateCredentials(w http.ResponseWriter, r *http.Request) {
	var req awsCredentialsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AccessKeyID == "" || req.SecretAccessKey == "" {
		jsonError(w, "access_key_id and secret_access_key are required", http.StatusBadRequest)
		return
	}
	if req.Environment != "com" && req.Environment != "gov" {
		jsonError(w, "environment must be 'com' or 'gov'", http.StatusBadRequest)
		return
	}

	// Dev mode: skip validation.
	if s.cfg.DevMode {
		sess := middleware.GetSession(r)
		sess.AWSAccessKeyID = req.AccessKeyID
		sess.AWSSecretAccessKey = req.SecretAccessKey
		sess.AWSSessionToken = req.SessionToken
		sess.AWSEnvironment = req.Environment
		sess.AWSAccountID = "dev-mode"
		if err := middleware.SaveSession(w, s.ses, sess); err != nil {
			jsonError(w, "failed to save session", http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]string{"status": "ok", "account_id": "dev-mode"})
		return
	}

	region, err := resolveRegion(req.Region, req.Environment)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Validate that the region belongs to the declared environment.
	detectedEnv, err := gorgaws.EnvFromRegion(region)
	if err != nil {
		jsonError(w, fmt.Sprintf("unknown region %q", region), http.StatusBadRequest)
		return
	}
	if detectedEnv != req.Environment {
		jsonError(w, fmt.Sprintf("region %s belongs to %s environment, not %s", region, detectedEnv, req.Environment), http.StatusBadRequest)
		return
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(req.AccessKeyID, req.SecretAccessKey, req.SessionToken),
		),
	)
	if err != nil {
		jsonError(w, "failed to build AWS config", http.StatusInternalServerError)
		return
	}

	identity, err := sts.NewFromConfig(cfg).GetCallerIdentity(r.Context(), &sts.GetCallerIdentityInput{})
	if err != nil {
		jsonError(w, "AWS credential validation failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	sess := middleware.GetSession(r)
	sess.AWSAccessKeyID = req.AccessKeyID
	sess.AWSSecretAccessKey = req.SecretAccessKey
	sess.AWSSessionToken = req.SessionToken
	sess.AWSEnvironment = req.Environment
	sess.AWSAccountID = aws.ToString(identity.Account)
	if err := middleware.SaveSession(w, s.ses, sess); err != nil {
		jsonError(w, "failed to save session", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]string{
		"status":     "ok",
		"account_id": aws.ToString(identity.Account),
		"user_id":    aws.ToString(identity.UserId),
		"arn":        aws.ToString(identity.Arn),
	})
}

func (s *Server) handleGetCredentials(w http.ResponseWriter, r *http.Request) {
	env := r.PathValue("environment")
	sess := middleware.GetSession(r)
	if !sess.HasAWSCredentials(env) {
		jsonError(w, "no credentials found for environment: "+env, http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]string{
		"environment": sess.AWSEnvironment,
		"status":      "active",
	})
}

func (s *Server) handleDeleteCredentials(w http.ResponseWriter, r *http.Request) {
	middleware.ClearSession(w, s.ses)
	jsonOK(w, map[string]string{"status": "ok"})
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	jsonOK(w, map[string]any{
		"authenticated":   sess.AWSAccessKeyID != "",
		"environment":     sess.AWSEnvironment,
		"session_created": sess.CreatedAt,
		"warning_minutes": s.cfg.SessionWarningMinutes,
	})
}

// resolveRegion returns the effective region, falling back to the default for the environment.
func resolveRegion(region, env string) (string, error) {
	if region != "" {
		return region, nil
	}
	switch env {
	case "com":
		return "us-east-1", nil
	case "gov":
		return "us-gov-west-1", nil
	default:
		return "", fmt.Errorf("unknown environment %q", env)
	}
}
