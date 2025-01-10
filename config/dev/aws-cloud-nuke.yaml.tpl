# This config file is used by cloud-nuke to clean up named resources associated
# with a specific managed cluster across an AWS account. CLUSTER_NAME is
# typically the metadata.name of the ClusterDeployment.
# The resources listed here are ALL of the potential resources that can be
# filtered by cloud-nuke, except for IAM resources since we'll never touch those.
# See: https://github.com/gruntwork-io/cloud-nuke?tab=readme-ov-file#whats-supported
#
# Usage:
# - 'CLUSTER_NAME=foo make dev-aws-nuke' will nuke resources affiliated with an AWS cluster named 'foo'
# Check cluster names with 'kubectl get clusterdeployment.k0rdent.mirantis.com -n hmc-system'

ACM:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
APIGateway:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
APIGatewayV2:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
AccessAnalyzer:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
AutoScalingGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
AppRunnerService:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
BackupVault:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
CloudWatchAlarm:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
CloudWatchDashboard:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
CloudWatchLogGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
CloudtrailTrail:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
CodeDeployApplications:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ConfigServiceRecorder:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ConfigServiceRule:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
DataSyncTask:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
DynamoDB:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EBSVolume:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ElasticBeanstalk:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2DedicatedHosts:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2KeyPairs:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2IPAM:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2IPAMPool:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2IPAMResourceDiscovery:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2IPAMScope:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2PlacementGroups:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2Subnet:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EC2Endpoint:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ECRRepository:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ECSCluster:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ECSService:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EKSCluster:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ELBv1:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ELBv2:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ElasticFileSystem:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ElasticIP:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
Elasticache:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ElasticacheParameterGroups:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
ElasticacheSubnetGroups:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
InternetGateway:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
EgressOnlyInternetGateway:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
LambdaFunction:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
LaunchConfiguration:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
LaunchTemplate:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
MSKCluster:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
NatGateway:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
NetworkACL:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
NetworkInterface:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
OIDCProvider:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
OpenSearchDomain:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
Redshift:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
DBClusters:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
DBInstances:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
RdsParameterGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
DBSubnetGroups:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
RDSProxy:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
s3:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
s3AccessPoint:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
S3ObjectLambdaAccessPoint:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
S3MultiRegionAccessPoint:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SecurityGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SesConfigurationset:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SesEmailTemplates:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SesIdentity:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SesReceiptRuleSet:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SesReceiptFilter:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SNS:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SQS:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SageMakerNotebook:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
SecretsManager:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
VPC:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
Route53HostedZone:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
Route53CIDRCollection:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
Route53TrafficPolicy:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
NetworkFirewall:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
NetworkFirewallPolicy:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
NetworkFirewallRuleGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
NetworkFirewallTLSConfig:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
NetworkFirewallResourcePolicy:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
VPCLatticeService:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
VPCLatticeServiceNetwork:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
VPCLatticeTargetGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME}.*'
