// Package credentials builds aws.Config values from session credentials.
// It is the bridge between the session layer and the gorg-aws visitor API,
// which requires a base aws.Config with a region set.
package credentials

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	gorgaws "github.com/toddwbucy/gorg-aws"
)

// Identity holds the result of a STS GetCallerIdentity call.
type Identity struct {
	AccountID string
	UserID    string
	ARN       string
}

// FromSession builds an aws.Config from the credentials stored in the session.
// Returns the config and the home region for that environment.
// The home region is required by gorg-aws's VisitOrganization and DryRun.
func FromSession(ctx context.Context, sess *middleware.Session) (aws.Config, string, error) {
	if sess.AWSAccessKeyID == "" || sess.AWSSecretAccessKey == "" {
		return aws.Config{}, "", fmt.Errorf("no AWS credentials in session")
	}
	region, err := HomeRegion(sess.AWSEnvironment)
	if err != nil {
		return aws.Config{}, "", err
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			sess.AWSAccessKeyID,
			sess.AWSSecretAccessKey,
			sess.AWSSessionToken,
		)),
	)
	if err != nil {
		return aws.Config{}, "", fmt.Errorf("building AWS config: %w", err)
	}
	return cfg, region, nil
}

// Validate calls STS GetCallerIdentity to confirm the credentials are valid.
func Validate(ctx context.Context, cfg aws.Config) (Identity, error) {
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return Identity{}, fmt.Errorf("validating credentials: %w", err)
	}
	return Identity{
		AccountID: aws.ToString(out.Account),
		UserID:    aws.ToString(out.UserId),
		ARN:       aws.ToString(out.Arn),
	}, nil
}

// HomeRegion returns the default home region for a given AWS environment string.
func HomeRegion(env string) (string, error) {
	switch env {
	case "com":
		return "us-east-1", nil
	case "gov":
		return "us-gov-west-1", nil
	default:
		return "", fmt.Errorf("%w: unknown environment %q", gorgaws.ErrInvalidEnv, env)
	}
}
