// Package ec2 provides EC2 instance discovery helpers.
// The Python original used boto3 describe_instances; this uses the AWS SDK v2
// paginator which handles NextToken automatically.
package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Instance is a lightweight descriptor for an EC2 instance.
type Instance struct {
	InstanceID string
	Platform   string // "linux" or "windows"
	State      string
	Name       string
	AccountID  string
	Region     string
}

// ListRunning returns all running EC2 instances visible from cfg.
// cfg.Region determines which regional endpoint is called.
// accountID is metadata attached to each returned Instance.
func ListRunning(ctx context.Context, cfg aws.Config, accountID string) ([]Instance, error) {
	client := ec2.NewFromConfig(cfg)
	pager := ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		},
	})

	var instances []Instance
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("DescribeInstances (%s): %w", cfg.Region, err)
		}
		for _, res := range page.Reservations {
			for _, inst := range res.Instances {
				instances = append(instances, Instance{
					InstanceID: aws.ToString(inst.InstanceId),
					Platform:   normalizePlatform(inst.Platform),
					State:      string(inst.State.Name),
					Name:       nameTag(inst.Tags),
					AccountID:  accountID,
					Region:     cfg.Region,
				})
			}
		}
	}
	return instances, nil
}

func normalizePlatform(p ec2types.PlatformValues) string {
	if p == ec2types.PlatformValuesWindows {
		return "windows"
	}
	return "linux"
}

func nameTag(tags []ec2types.Tag) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == "Name" {
			return aws.ToString(t.Value)
		}
	}
	return ""
}
