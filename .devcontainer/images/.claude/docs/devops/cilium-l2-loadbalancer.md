# Cilium L2 LoadBalancer Pattern

## Overview

Cilium L2 announcements enable LoadBalancer services to work without cloud provider integration. This pattern uses ARP/NDP announcements to expose services on local network segments, ideal for bare-metal and on-premise deployments.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Physical Network                          │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐        │
│  │  eno1   │  │  eno2   │  │  eno1   │  │  eno2   │        │
│  │Internal │  │ Public  │  │Internal │  │ Public  │        │
│  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘        │
│       │            │            │            │              │
│  ┌────┴────────────┴────┐ ┌────┴────────────┴────┐         │
│  │      Node 1          │ │      Node 2          │         │
│  │  ┌──────────────┐    │ │  ┌──────────────┐    │         │
│  │  │   Cilium     │    │ │  │   Cilium     │    │         │
│  │  │  L2 Agent    │    │ │  │  L2 Agent    │    │         │
│  │  └──────────────┘    │ │  └──────────────┘    │         │
│  └──────────────────────┘ └──────────────────────┘         │
└─────────────────────────────────────────────────────────────┘
```

## Configuration

### Multi-Interface Network Definition

```hcl
# =============================================================================
# NETWORK CONFIGURATION VARIABLES
# =============================================================================
# Define network interfaces and their corresponding IP ranges

locals {
  network_interfaces = {
    eno1 = {
      interface_name = "eno1"
      network_type   = "internal"
      ip_range = {
        start = "192.168.3.150"
        stop  = "192.168.3.250"
      }
      # Empty node selector means all nodes
      node_selector = {}
      description   = "Internal network for cluster services"
    }
    eno2 = {
      interface_name = "eno2"
      network_type   = "public-api"
      ip_range = {
        start = "192.168.4.150"
        stop  = "192.168.4.250"
      }
      # Restrict to control plane nodes for security
      node_selector = {
        "node.kubernetes.io/control-plane" = ""
      }
      description = "Public API network for external services"
    }
  }
}
```

### Cilium Helm Values

```yaml
# =============================================================================
# CILIUM HELM VALUES - L2 LoadBalancer Configuration
# =============================================================================

# API Server Connection
k8sServiceHost: "kube.internal"
k8sServicePort: "6443"

# CNI Configuration
cni:
  install: true
  binPath: "/opt/cni/bin"
  confPath: "/etc/cni/net.d"
  exclusive: false

# Core Networking
kubeProxyReplacement: true
routingMode: "tunnel"
enableIPv4Masquerade: true

# L2 Announcements - CRITICAL for bare-metal LoadBalancer
l2announcements:
  enabled: true

# IPAM Configuration
ipam:
  mode: "cluster-pool"
  operator:
    clusterPoolIPv4PodCIDRList: ["10.0.0.0/16"]
    clusterPoolIPv4MaskSize: 24

# BPF Configuration (for CoreOS/Flatcar)
bpf:
  autoMount:
    enabled: true
  mountPath: "/sys/fs/bpf"

# Operator Configuration
operator:
  replicas: 1
  tolerations:
    - operator: Exists
  nodeSelector:
    node-role.kubernetes.io/control-plane: ""

# Node Init for non-standard CNI paths
nodeInit:
  enabled: true
  restartPods: false

# Tolerations for all nodes
tolerations:
  - operator: Exists
```

### L2 Announcement Policy (CRD)

```hcl
resource "kubectl_manifest" "cilium_l2_policies" {
  for_each = local.network_interfaces

  yaml_body = yamlencode({
    apiVersion = "cilium.io/v2alpha1"
    kind       = "CiliumL2AnnouncementPolicy"
    metadata = {
      name = "l2-policy-${each.key}"
      labels = {
        "app.kubernetes.io/managed-by"    = "terraform"
        "app.kubernetes.io/component"     = "cilium-l2-policy"
        "network.example.com/interface"   = each.key
        "network.example.com/type"        = each.value.network_type
      }
    }
    spec = {
      serviceSelector = {
        matchLabels = {
          "network.example.com/interface" = each.key
        }
      }
      nodeSelector = {
        matchLabels = each.value.node_selector
      }
      interfaces = [each.value.interface_name]
      externalIPs = true
      loadBalancerIPs = true
    }
  })
}
```

### IP Pool Configuration

```hcl
resource "kubectl_manifest" "cilium_ip_pools" {
  for_each = local.network_interfaces

  yaml_body = yamlencode({
    apiVersion = "cilium.io/v2alpha1"
    kind       = "CiliumLoadBalancerIPPool"
    metadata = {
      name = "ip-pool-${each.key}"
      labels = {
        "app.kubernetes.io/managed-by"    = "terraform"
        "app.kubernetes.io/component"     = "cilium-ip-pool"
        "network.example.com/interface"   = each.key
        "network.example.com/type"        = each.value.network_type
      }
    }
    spec = {
      blocks = [
        {
          start = each.value.ip_range.start
          stop  = each.value.ip_range.stop
        }
      ]
      serviceSelector = {
        matchLabels = {
          "network.example.com/interface" = each.key
        }
      }
    }
  })
}
```

## Usage

### Creating a LoadBalancer Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-service
  labels:
    network.example.com/interface: eno1  # Select internal network
spec:
  type: LoadBalancer
  selector:
    app: my-app
  ports:
    - port: 80
      targetPort: 8080
```

### Multi-Interface Service

```yaml
# Internal service
apiVersion: v1
kind: Service
metadata:
  name: api-internal
  labels:
    network.example.com/interface: eno1
spec:
  type: LoadBalancer
  selector:
    app: api
  ports:
    - port: 8080
---
# External service (same app, different interface)
apiVersion: v1
kind: Service
metadata:
  name: api-external
  labels:
    network.example.com/interface: eno2
spec:
  type: LoadBalancer
  selector:
    app: api
  ports:
    - port: 443
```

## Istio Ambient Compatibility

When using with Istio Ambient mesh, disable Cilium's L7 features:

```yaml
# Disable for Istio Ambient compatibility
envoy:
  enabled: false
l7Proxy: false
socketLB:
  enabled: true
  hostNamespaceOnly: true
nodePort:
  enabled: true

# Disable network policies (Istio handles this)
policyEnforcementMode: "never"
k8sNetworkPolicy:
  enabled: false
```

## Troubleshooting

### Check L2 Policy Status

```bash
kubectl get ciliuml2announcementpolicy
kubectl describe ciliuml2announcementpolicy l2-policy-eno1
```

### Check IP Pool Status

```bash
kubectl get ciliumloadbalancerippool
kubectl describe ciliumloadbalancerippool ip-pool-eno1
```

### Verify Service IP Assignment

```bash
kubectl get svc -o wide
kubectl get ciliumendpoint
```

### Debug ARP Announcements

```bash
# On the node
cilium-dbg bgp peers
cilium-dbg service list
```

## Best Practices

1. **Interface Isolation** - Use separate interfaces for internal vs external traffic
2. **Node Selectors** - Restrict public IPs to control plane or dedicated ingress nodes
3. **IP Range Planning** - Reserve sufficient IPs per interface, avoid overlap
4. **Labels Convention** - Use consistent labeling for network/interface selection
5. **Security** - Control plane nodes for external services, workers for internal

## Related Patterns

- [Immutable Infrastructure](./immutable-infrastructure.md)
- [Blue-Green Deployment](./blue-green.md)
- [GitOps](./gitops.md)
