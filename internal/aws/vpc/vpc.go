// Package vpc provides EC2 VPC query primitives.
// These back the VPC recon workflow and any future VPC-related workflow —
// the frontend configures which filters to apply and how to display results.
package vpc

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// VPC summarises a single VPC.
type VPC struct {
	ID        string            `json:"id"`
	CIDRBlock string            `json:"cidr_block"`
	IsDefault bool              `json:"is_default"`
	State     string            `json:"state"`
	Tags      map[string]string `json:"tags"`
}

// Subnet summarises a single subnet.
type Subnet struct {
	ID               string            `json:"id"`
	VPCID            string            `json:"vpc_id"`
	CIDRBlock        string            `json:"cidr_block"`
	AvailabilityZone string            `json:"availability_zone"`
	AvailableIPs     int32             `json:"available_ips"`
	Tags             map[string]string `json:"tags"`
}

// SecurityGroup summarises a single security group.
type SecurityGroup struct {
	ID          string            `json:"id"`
	VPCID       string            `json:"vpc_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Tags        map[string]string `json:"tags"`
}

// Snapshot holds all VPC-related data for one account/region.
type Snapshot struct {
	AccountID      string          `json:"account_id"`
	Region         string          `json:"region"`
	VPCs           []VPC           `json:"vpcs"`
	Subnets        []Subnet        `json:"subnets"`
	SecurityGroups []SecurityGroup `json:"security_groups"`
}

// Describe returns a full VPC snapshot for the account/region in cfg.
// Passing vpcIDs scopes all queries to those VPCs; nil returns everything.
func Describe(ctx context.Context, cfg aws.Config, accountID string, vpcIDs []string) (*Snapshot, error) {
	client := ec2.NewFromConfig(cfg)

	var vpcFilter []ec2types.Filter
	if len(vpcIDs) > 0 {
		vpcFilter = []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: vpcIDs},
		}
	}

	snap := &Snapshot{AccountID: accountID, Region: cfg.Region}

	// VPCs
	vpcsOut, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{Filters: vpcFilter})
	if err != nil {
		return nil, fmt.Errorf("DescribeVpcs (%s/%s): %w", accountID, cfg.Region, err)
	}
	for _, v := range vpcsOut.Vpcs {
		snap.VPCs = append(snap.VPCs, VPC{
			ID:        aws.ToString(v.VpcId),
			CIDRBlock: aws.ToString(v.CidrBlock),
			IsDefault: aws.ToBool(v.IsDefault),
			State:     string(v.State),
			Tags:      tagsToMap(v.Tags),
		})
	}

	// Subnets
	subnetsOut, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{Filters: vpcFilter})
	if err != nil {
		return nil, fmt.Errorf("DescribeSubnets (%s/%s): %w", accountID, cfg.Region, err)
	}
	for _, s := range subnetsOut.Subnets {
		snap.Subnets = append(snap.Subnets, Subnet{
			ID:               aws.ToString(s.SubnetId),
			VPCID:            aws.ToString(s.VpcId),
			CIDRBlock:        aws.ToString(s.CidrBlock),
			AvailabilityZone: aws.ToString(s.AvailabilityZone),
			AvailableIPs:     aws.ToInt32(s.AvailableIpAddressCount),
			Tags:             tagsToMap(s.Tags),
		})
	}

	// Security groups
	sgsOut, err := client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: vpcFilter})
	if err != nil {
		return nil, fmt.Errorf("DescribeSecurityGroups (%s/%s): %w", accountID, cfg.Region, err)
	}
	for _, sg := range sgsOut.SecurityGroups {
		snap.SecurityGroups = append(snap.SecurityGroups, SecurityGroup{
			ID:          aws.ToString(sg.GroupId),
			VPCID:       aws.ToString(sg.VpcId),
			Name:        aws.ToString(sg.GroupName),
			Description: aws.ToString(sg.Description),
			Tags:        tagsToMap(sg.Tags),
		})
	}

	return snap, nil
}

func tagsToMap(tags []ec2types.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return m
}
