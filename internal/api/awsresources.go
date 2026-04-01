package api

import (
	"net/http"

	awscreds "github.com/toddwbucy/GOrg-CloudTools/internal/aws/credentials"
	"github.com/toddwbucy/GOrg-CloudTools/internal/aws/ec2"
	"github.com/toddwbucy/GOrg-CloudTools/internal/aws/vpc"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
)

// handleListInstances returns running EC2 instances for the given account and region.
// Query params: account_id, region, platform ("linux"|"windows", optional filter)
func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	accountID := q.Get("account_id")
	region := q.Get("region")
	platform := q.Get("platform")

	if accountID == "" || region == "" {
		jsonError(w, "account_id and region are required", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}
	cfg.Region = region

	instances, err := ec2.ListRunning(r.Context(), cfg, accountID)
	if err != nil {
		jsonError(w, "failed to list instances: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Optional platform filter.
	if platform != "" {
		filtered := instances[:0]
		for _, inst := range instances {
			if inst.Platform == platform {
				filtered = append(filtered, inst)
			}
		}
		instances = filtered
	}

	jsonOK(w, map[string]any{"instances": instances})
}

// handleDescribeVPCs returns VPCs, subnets, and security groups for the given account and region.
// Query params: account_id, region, vpc_id (optional, repeatable)
func (s *Server) handleDescribeVPCs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	accountID := q.Get("account_id")
	region := q.Get("region")
	vpcIDs := q["vpc_id"] // zero or more

	if accountID == "" || region == "" {
		jsonError(w, "account_id and region are required", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}
	cfg.Region = region

	snap, err := vpc.Describe(r.Context(), cfg, accountID, vpcIDs)
	if err != nil {
		jsonError(w, "failed to describe VPCs: "+err.Error(), http.StatusBadGateway)
		return
	}

	jsonOK(w, snap)
}

// handleOrgAccounts returns the list of accounts in the org (or under a given OU)
// without assuming any roles. Uses gorg-aws DryRun.
// Query params: env ("com"|"gov"), parent_id (optional OU ID)
func (s *Server) handleOrgAccounts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	env := q.Get("env")
	parentID := q.Get("parent_id")

	if env != "com" && env != "gov" {
		jsonError(w, "env must be 'com' or 'gov'", http.StatusBadRequest)
		return
	}

	runner := s.orgRunners[env]
	if runner == nil {
		jsonError(w, "org access is not configured for env "+env+" (management credentials required)", http.StatusServiceUnavailable)
		return
	}

	accounts, regions, err := runner.DryRun(r.Context(), env, parentID)
	if err != nil {
		jsonError(w, "org dry-run failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	jsonOK(w, map[string]any{
		"accounts": accounts,
		"regions":  regions,
	})
}
