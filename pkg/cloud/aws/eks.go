package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/eks"
	"github.com/aws/aws-sdk-go/service/sts"

	"github.com/k11n/konstellation/api/v1alpha1"
	"github.com/k11n/konstellation/pkg/cloud/types"
	"github.com/k11n/konstellation/pkg/utils/async"
)

const (
	// By default STS signs the url for 15 minutes so we are creating a
	// rfc3339 timestamp with expiration in 14 minutes as part of the token, which
	// is used by some clients (client-go) who will refresh the token after 14 mins
	TOKEN_EXPIRATION_MINS = 14
	URL_TIMEOUT_SECONDS   = 60
)

var (
	statusMapping = map[string]types.ClusterStatus{
		"CREATING": types.StatusCreating,
		"ACTIVE":   types.StatusActive,
		"DELETING": types.StatusDeleting,
		"FAILED":   types.StatusFailed,
		"UPDATING": types.StatusUpdating,
	}
)

type EKSService struct {
	session *session.Session
	EKS     *eks.EKS
}

func NewEKSService(s *session.Session) *EKSService {
	return &EKSService{
		session: s,
		EKS:     eks.New(s),
	}
}

func (s *EKSService) GetAvailableVersions(ctx context.Context) ([]string, error) {
	return EKSAvailableVersions, nil
}

func (s *EKSService) ListClusters(ctx context.Context) (clusters []*types.Cluster, err error) {
	var clusterNames []*string
	err = s.EKS.ListClustersPagesWithContext(ctx, &eks.ListClustersInput{}, func(output *eks.ListClustersOutput, b bool) bool {
		for _, clusterName := range output.Clusters {
			clusterNames = append(clusterNames, clusterName)
		}
		return true

	})
	if err != nil {
		return
	}

	wp := async.NewWorkerPool()

	// describe each cluster
	for i, _ := range clusterNames {
		clusterName := clusterNames[i]
		wp.AddTask(func() (interface{}, error) {
			descOut, err := s.EKS.DescribeClusterWithContext(ctx, &eks.DescribeClusterInput{
				Name: clusterName,
			})
			if err != nil {
				return nil, err
			}
			if descOut.Cluster.Tags[TagKonstellation] != nil && *descOut.Cluster.Tags[TagKonstellation] == TagValue1 {
				return clusterFromEksCluster(descOut.Cluster), nil
			}
			return nil, nil
		})
	}
	wp.StopWait()

	for _, t := range wp.GetTasks() {
		if t.Err != nil {
			return nil, t.Err
		}
		if t.Result == nil {
			continue
		}
		clusters = append(clusters, t.Result.(*types.Cluster))
	}

	return
}

func (s *EKSService) GetCluster(ctx context.Context, name string) (cluster *types.Cluster, err error) {
	descOut, err := s.EKS.DescribeClusterWithContext(ctx, &eks.DescribeClusterInput{
		Name: &name,
	})
	if err != nil {
		return
	}
	cluster = clusterFromEksCluster(descOut.Cluster)
	return
}

func (s *EKSService) GetAuthToken(ctx context.Context, cluster string, status types.ClusterStatus) (authToken *types.AuthToken, err error) {
	var stsSvc *sts.STS

	// when fully configured, we should always be using the admin role when providing kube auth token
	// normally with EKS only the user who's created the cluster has access
	if status == types.StatusActive {
		stsSvc = sts.New(s.session)
		// get current user and account info
		res, err := stsSvc.GetCallerIdentityWithContext(ctx, &sts.GetCallerIdentityInput{})
		if err != nil {
			return nil, err
		}

		roleArn := arn.ARN{
			Partition: "aws",
			Service:   "iam",
			AccountID: *res.Account,
			Resource:  fmt.Sprintf("role/kon-%s-admin-role", cluster),
		}

		callerArn, err := arn.Parse(*res.Arn)
		if err != nil {
			return nil, err
		}
		parts := strings.Split(callerArn.Resource, "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("unexpected AWS identity: %s, expected user/<name>", callerArn.Resource)
		}
		userName := parts[1]
		creds := stscreds.NewCredentials(s.session, roleArn.String(), func(p *stscreds.AssumeRoleProvider) {
			p.RoleSessionName = userName
		})

		// create new STS with the role
		stsSvc = sts.New(s.session, &aws.Config{Credentials: creds})
	} else {
		stsSvc = sts.New(s.session)
	}

	req, _ := stsSvc.GetCallerIdentityRequest(&sts.GetCallerIdentityInput{})
	req.HTTPRequest.Header.Set("x-k8s-aws-id", cluster)
	signedUrl, err := req.Presign(URL_TIMEOUT_SECONDS * time.Second)
	if err != nil {
		return
	}
	// fmt.Printf("signed url: %s\n", signedUrl)
	encoded := strings.TrimRight(
		base64.URLEncoding.EncodeToString([]byte(signedUrl)),
		"=",
	)

	authToken = &types.AuthToken{
		Kind:       "ExecCredential",
		ApiVersion: "client.authentication.k8s.io/v1alpha1",
		Spec:       make(map[string]interface{}),
	}

	expTime := time.Now().UTC().Add(TOKEN_EXPIRATION_MINS * time.Minute)
	authToken.Status.ExpirationTimestamp = types.RFC3339Time(expTime)
	authToken.Status.Token = fmt.Sprintf("k8s-aws-v1.%s", encoded)
	return
}

func (s *EKSService) IsNodepoolReady(ctx context.Context, clusterName string, nodepoolName string) (ready bool, err error) {
	res, err := s.EKS.DescribeNodegroupWithContext(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   &clusterName,
		NodegroupName: &nodepoolName,
	})
	if err != nil {
		return
	}
	// https://github.com/aws/aws-sdk-go/blob/ab52e2140da6138c05220ee782cc2bcd85feecee/models/apis/eks/2017-11-01/api-2.json#L1048
	ready = *res.Nodegroup.Status == "ACTIVE"
	return
}

func (s *EKSService) IsNodepoolDeleted(ctx context.Context, clusterName string, nodepoolName string) (deleted bool, err error) {
	res, err := s.EKS.ListNodegroupsWithContext(ctx, &eks.ListNodegroupsInput{
		ClusterName: &clusterName,
		MaxResults:  aws.Int64(DefaultPageSize),
	})
	if err != nil {
		return
	}

	for _, item := range res.Nodegroups {
		if *item == nodepoolName {
			return false, nil
		}
	}

	return true, nil
}

func (s *EKSService) DeleteNodeGroupNetworkingResources(ctx context.Context, nodegroup string) error {
	ec2Svc := ec2.New(s.session)

	sgs, err := ec2Svc.DescribeSecurityGroupsWithContext(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String(fmt.Sprintf("tag:%s", TagEKSNodeGroupName)),
				Values: []*string{&nodegroup},
			},
		}})
	if err != nil {
		return err
	}

	var groupIds []*string
	for _, sg := range sgs.SecurityGroups {
		groupIds = append(groupIds, sg.GroupId)
	}

	if len(groupIds) == 0 {
		return nil
	}

	// find all network interfaces and delete
	niRes, err := ec2Svc.DescribeNetworkInterfacesWithContext(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("group-id"),
				Values: groupIds,
			},
		},
	})
	if err != nil {
		return err
	}

	isSuccess := true
	for _, ni := range niRes.NetworkInterfaces {
		// ignore errors
		_, err := ec2Svc.DeleteNetworkInterfaceWithContext(ctx, &ec2.DeleteNetworkInterfaceInput{
			NetworkInterfaceId: ni.NetworkInterfaceId,
		})
		if err != nil {
			isSuccess = false
		}
	}

	if isSuccess {
		// delete security groups
		for _, groupId := range groupIds {
			ec2Svc.DeleteSecurityGroupWithContext(ctx, &ec2.DeleteSecurityGroupInput{
				GroupId: groupId,
			})
		}
	}
	return nil
}

func (s *EKSService) CreateNodepool(ctx context.Context, cc *v1alpha1.ClusterConfig, np *v1alpha1.Nodepool) error {
	createInput, err := nodepoolSpecToCreateInput(cc, np)
	if err != nil {
		return err
	}
	_, err = s.EKS.CreateNodegroupWithContext(ctx, createInput)
	return err
}

func (s *EKSService) DeleteNodepool(ctx context.Context, clusterName string, nodePool string) error {
	_, err := s.EKS.DeleteNodegroupWithContext(ctx, &eks.DeleteNodegroupInput{
		ClusterName:   &clusterName,
		NodegroupName: &nodePool,
	})
	return err
}

func (s *EKSService) TagSubnetsForCluster(ctx context.Context, clusterName string, subnetIds []string) error {
	// tag VPC subnets if needed
	ec2Svc := ec2.New(s.session)
	for _, subnet := range subnetIds {
		_, err := ec2Svc.CreateTagsWithContext(ctx, &ec2.CreateTagsInput{
			Resources: []*string{aws.String(subnet)},
			Tags: []*ec2.Tag{
				{
					Key:   aws.String(KubeClusterTag(clusterName)),
					Value: aws.String(TagValueShared),
				},
			},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *EKSService) UnTagSubnetsForCluster(ctx context.Context, clusterName string, subnetIds []string) error {
	// untag VPC subnets if needed
	ec2Svc := ec2.New(s.session)
	resourcesToTag := make([]*string, 0, len(subnetIds))
	for _, subnet := range subnetIds {
		resourcesToTag = append(resourcesToTag, &subnet)
	}

	if len(resourcesToTag) == 0 {
		return nil
	}

	_, err := ec2Svc.DeleteTagsWithContext(ctx, &ec2.DeleteTagsInput{
		Resources: resourcesToTag,
		Tags: []*ec2.Tag{
			{
				Key: aws.String(KubeClusterTag(clusterName)),
			},
		},
	})

	return err
}

func nodepoolSpecToCreateInput(cc *v1alpha1.ClusterConfig, np *v1alpha1.Nodepool) (cni *eks.CreateNodegroupInput, err error) {
	awsStatus := cc.Status.AWS
	if awsStatus == nil {
		err = fmt.Errorf("Nodepool creation requires ClusterConfig.AWS, which is nil")
		return
	}
	nps := np.Spec
	cni = &eks.CreateNodegroupInput{}
	cni.SetClusterName(cc.Name)
	cni.SetAmiType(nps.AWS.AMIType)
	cni.SetDiskSize(int64(nps.DiskSizeGiB))
	cni.SetInstanceTypes([]*string{&nps.MachineType})
	cni.SetNodeRole(awsStatus.NodeRoleArn)
	cni.SetNodegroupName(np.ObjectMeta.Name)
	cni.SetScalingConfig(&eks.NodegroupScalingConfig{
		MinSize:     &nps.MinSize,
		MaxSize:     &nps.MaxSize,
		DesiredSize: &nps.MinSize,
	})
	var subnetSrc []*v1alpha1.AWSSubnet
	if cc.Spec.AWS.Topology == v1alpha1.NetworkTopologyPublicPrivate {
		subnetSrc = awsStatus.PrivateSubnets
	} else {
		subnetSrc = awsStatus.PublicSubnets
	}

	for _, subnet := range subnetSrc {
		cni.Subnets = append(cni.Subnets, &subnet.SubnetId)
	}

	if nps.AWS.SSHKeypair != "" {
		rac := eks.RemoteAccessConfig{
			Ec2SshKey: &nps.AWS.SSHKeypair,
		}

		if !nps.AWS.ConnectFromAnywhere {
			groups := make([]*string, 0, len(awsStatus.SecurityGroups))
			for _, g := range awsStatus.SecurityGroups {
				groups = append(groups, &g)
			}
			rac.SetSourceSecurityGroups(groups)
		}
		cni.SetRemoteAccess(&rac)
	}

	tags := make(map[string]*string)
	if nps.Autoscale {
		tags[AutoscalerClusterNameTag(cc.Name)] = aws.String(TagValueOwned)
		tags[TagAutoscalerEnabled] = aws.String(TagValueTrue)
	}
	cni.SetTags(tags)

	return
}

func clusterFromEksCluster(ec *eks.Cluster) *types.Cluster {
	cluster := &types.Cluster{
		ID:              *ec.Arn,
		CloudProvider:   "aws",
		Name:            *ec.Name,
		PlatformVersion: *ec.PlatformVersion,
		Status:          statusMapping[*ec.Status],
		Version:         *ec.Version,
	}
	if ec.Endpoint != nil {
		cluster.Endpoint = *ec.Endpoint
	}
	if ec.CertificateAuthority != nil && ec.CertificateAuthority.Data != nil {
		var decoded []byte
		decoded, err := base64.StdEncoding.DecodeString(*ec.CertificateAuthority.Data)
		if err == nil {
			cluster.CertificateAuthorityData = decoded
		}
	}

	// consider unactivated clusters as unconfigured
	if cluster.Status == types.StatusActive {
		if ec.Tags[TagClusterActivated] == nil {
			cluster.Status = types.StatusUnconfigured
		}
	}
	return cluster
}

func GetEC2Tag(tags []*ec2.Tag, name string) *ec2.Tag {
	for _, tag := range tags {
		if *tag.Key == name {
			return tag
		}
	}
	return nil
}
