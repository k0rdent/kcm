# This config file is used by cloud-nuke to clean up named resources associated
# with managed clusters whose names share CLUSTER_NAME_PREFIX. The escaped
# prefix is provided as CLUSTER_NAME_REGEX for boundary-safe regex filters.
# The resources listed here are a safe superset of the types selected by
# dev-aws-nuke. Unselected types are ignored by cloud-nuke.
# See: https://github.com/gruntwork-io/cloud-nuke/blob/master/docs/supported-resources.md
#
# Usage:
# - 'CLUSTER_NAME_PREFIX=foo make dev-aws-nuke' will nuke resources affiliated with AWS clusters named 'foo-*'
# Check cluster names with 'kubectl get clusterdeployment.k0rdent.mirantis.com -n kcm-system'

ACM:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
APIGateway:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
APIGatewayV2:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
AccessAnalyzer:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
AutoScalingGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
AppRunnerService:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
BackupVault:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
CloudWatchAlarm:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
CloudWatchDashboard:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
CloudWatchLogGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
CloudTrailTrail:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
CodeDeployApplications:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ConfigServiceRecorder:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ConfigServiceRule:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
DataSyncTask:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
DynamoDB:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EBSVolume:
  include:
    tags:
      Name: '^${CLUSTER_NAME_REGEX}($|-).*'
      kubernetes.io/created-for/pvc/name: '^${CLUSTER_NAME_REGEX}($|-).*'
    tags_operator: OR
Snapshots:
  include:
    tags:
      Name: '^${CLUSTER_NAME_REGEX}($|-).*'
      kubernetes.io/created-for/pvc/name: '^${CLUSTER_NAME_REGEX}($|-).*'
    tags_operator: OR
ElasticBeanstalk:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2DedicatedHosts:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2KeyPairs:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2IPAM:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2IPAMPool:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2IPAMResourceDiscovery:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2IPAMScope:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2PlacementGroups:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2Subnet:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EC2Endpoint:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ECRRepository:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ECSCluster:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ECSService:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EKSCluster:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ELBv1:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ELBv2:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ElasticFileSystem:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ElasticIP:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ElastiCache:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ElastiCacheParameterGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
ElastiCacheSubnetGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
InternetGateway:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
EgressOnlyInternetGateway:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
LambdaFunction:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
LaunchConfiguration:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
LaunchTemplate:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
MSKCluster:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
NATGateway:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
NetworkACL:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
NetworkInterface:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
OIDCProvider:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
OpenSearchDomain:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
Redshift:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
DBClusters:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
DBInstances:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
RDSParameterGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
DBSubnetGroups:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
RDSProxy:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
S3:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
S3AccessPoint:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
S3ObjectLambdaAccessPoint:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
S3MultiRegionAccessPoint:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SecurityGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SESConfigurationSet:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SESEmailTemplates:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SESIdentity:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SESReceiptRuleSet:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SESReceiptFilter:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SNS:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SQS:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SageMakerNotebook:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
SecretsManager:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
VPC:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
Route53HostedZone:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
Route53CIDRCollection:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
Route53TrafficPolicy:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
NetworkFirewall:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
NetworkFirewallPolicy:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
NetworkFirewallRuleGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
NetworkFirewallTLSConfig:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
NetworkFirewallResourcePolicy:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
VPCLatticeService:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
VPCLatticeServiceNetwork:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
VPCLatticeTargetGroup:
  include:
    names_regex:
      - '^${CLUSTER_NAME_REGEX}($|-).*'
