# Hetzner Cloud Provider

This document describes how to use the Hetzner Cloud provider with KCM.

## Overview

The Hetzner Cloud provider enables you to deploy and manage Kubernetes clusters on Hetzner Cloud infrastructure. It supports both standalone and hosted control plane configurations.

## Prerequisites

1. A Hetzner Cloud account
2. An API token with read/write permissions
3. SSH key uploaded to Hetzner Cloud
4. The `hcloud` CLI tool (optional but recommended)

## Provider Types

### Standalone Control Plane

The standalone control plane configuration deploys both control plane nodes and worker nodes on Hetzner Cloud instances. This is suitable for:
- Production environments requiring full control over the control plane
- Scenarios where control plane customization is needed
- High-availability requirements with multiple control plane nodes

### Hosted Control Plane

The hosted control plane configuration manages only worker nodes, while the control plane is managed externally. This is suitable for:
- Development and testing environments
- Scenarios where simplified management is preferred
- Cost-optimized deployments

## Configuration

### Common Parameters

The following environment variables are required for both configurations:

```bash
# Required
export HCLOUD_TOKEN="your-token"
export HCLOUD_TOKEN_SECRET="hcloud-token"
export NAMESPACE="default"
export HCLOUD_REGION="fsn1"  # Available: fsn1, nbg1, hel1
export SSH_KEY_NAME="your-ssh-key"

# Optional with defaults
export WORKER_NODE_TYPE="cx21"  # Available: cx21, cx31, cx41, cx51
export NODE_IMAGE="ubuntu-22.04"
```

### Network Configuration

The provider creates a private network with the following default configuration:
```yaml
network:
  cidrBlock: "10.0.0.0/16"    # Network CIDR
  subnetCidrBlock: "10.0.0.0/24"  # Subnet CIDR
```

### Load Balancer

Load balancer configuration options:
```yaml
loadBalancer:
  enabled: true
  type: "lb11"  # Available: lb11, lb21, lb31
```

## Example Deployments

### 1. Basic Standalone Cluster

```yaml
# values.yaml
region: "fsn1"
sshKeyName: "my-ssh-key"
clusterIdentity:
  name: "hcloud-token"
  kind: "HetznerClusterIdentity"

controlPlaneNumber: 3
workersNumber: 2

worker:
  serverType: "cx21"
  image: "ubuntu-22.04"
  rootVolume:
    size: 40
```

### 2. High-Availability Production Cluster

```yaml
# values.yaml
region: "fsn1"
sshKeyName: "prod-ssh-key"
clusterIdentity:
  name: "hcloud-prod-token"
  kind: "HetznerClusterIdentity"

controlPlaneNumber: 5
workersNumber: 3

controlPlane:
  serverType: "cx31"
  image: "ubuntu-22.04"
  rootVolume:
    size: 80
  placementGroup:
    enabled: true
    type: "spread"

worker:
  serverType: "cx41"
  image: "ubuntu-22.04"
  rootVolume:
    size: 100
  placementGroup:
    enabled: true
    type: "spread"

loadBalancer:
  enabled: true
  type: "lb21"

network:
  cidrBlock: "172.16.0.0/16"
  subnetCidrBlock: "172.16.0.0/24"
```

### 3. Development Hosted Control Plane

```yaml
# values.yaml
region: "fsn1"
sshKeyName: "dev-ssh-key"
clusterIdentity:
  name: "hcloud-dev-token"
  kind: "HetznerClusterIdentity"

workersNumber: 2

worker:
  serverType: "cx21"
  image: "ubuntu-22.04"
  rootVolume:
    size: 40
```

## Server Types

Available server types and their specifications:

| Type | vCPUs | RAM | Storage | Use Case |
|------|--------|-----|---------|----------|
| cx21 | 2 | 4GB | 40GB | Development/Testing |
| cx31 | 2 | 8GB | 80GB | Small Production |
| cx41 | 4 | 16GB | 160GB | Medium Production |
| cx51 | 8 | 32GB | 240GB | Large Production |

## Best Practices

1. **High Availability**
   - Use at least 3 control plane nodes for production
   - Enable placement groups for node distribution
   - Use higher-tier load balancers for production

2. **Networking**
   - Plan your network CIDR to avoid conflicts
   - Consider future growth when sizing networks
   - Use private networks for inter-node communication

3. **Storage**
   - Size root volumes appropriately for workload
   - Consider backup strategies
   - Monitor disk usage

4. **Security**
   - Use dedicated API tokens per cluster
   - Rotate API tokens regularly
   - Keep SSH keys secure

## Troubleshooting

### Common Issues

1. **API Token Issues**:
   ```bash
   # Verify token permissions
   hcloud token verify

   # Check token in secret
   kubectl get secret $HCLOUD_TOKEN_SECRET -n $NAMESPACE -o yaml
   ```

2. **Network Issues**:
   ```bash
   # Check network creation
   hcloud network list
   
   # Verify subnet allocation
   hcloud network describe <network-name>
   ```

3. **Load Balancer Issues**:
   ```bash
   # List load balancers
   hcloud load-balancer list
   
   # Check load balancer health
   hcloud load-balancer describe <lb-name>
   ```

### Logging

To enable debug logging:
```yaml
k0s:
  api:
    extraArgs:
      v: "4"
```

## Support and Resources

- [Cluster API Provider Hetzner Documentation](https://github.com/syself/cluster-api-provider-hetzner)
- [Hetzner Cloud Documentation](https://docs.hetzner.com)
- [Hetzner Cloud API Documentation](https://docs.hetzner.cloud)
- [Hetzner Status Page](https://status.hetzner.com)

## Contributing

We welcome contributions to improve the Hetzner Cloud provider. Please submit issues and pull requests to the repository. 