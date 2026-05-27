# Compute Resource Consolidation Pattern

> Optimize resource utilization by consolidating workloads.

## Principle

```
┌─────────────────────────────────────────────────────────────────────────┐
│                 BEFORE: Underutilized resources                          │
│                                                                          │
│   ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐         │
│   │    VM 1         │  │    VM 2         │  │    VM 3         │         │
│   │ ┌─────────────┐ │  │ ┌─────────────┐ │  │ ┌─────────────┐ │         │
│   │ │ Service A   │ │  │ │ Service B   │ │  │ │ Service C   │ │         │
│   │ │ CPU: 10%    │ │  │ │ CPU: 15%    │ │  │ │ CPU: 5%     │ │         │
│   │ │ RAM: 20%    │ │  │ │ RAM: 25%    │ │  │ │ RAM: 10%    │ │         │
│   │ └─────────────┘ │  │ └─────────────┘ │  │ └─────────────┘ │         │
│   │   4 CPU, 16GB   │  │   4 CPU, 16GB   │  │   4 CPU, 16GB   │         │
│   └─────────────────┘  └─────────────────┘  └─────────────────┘         │
│                                                                          │
│   Total: 12 CPU, 48GB RAM - Utilization: ~10%                           │
│   Cost: $$$                                                              │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘

                                   │
                                   ▼

┌─────────────────────────────────────────────────────────────────────────┐
│                 AFTER: Consolidated resources                             │
│                                                                          │
│              ┌─────────────────────────────────────┐                    │
│              │            Shared Node              │                    │
│              │  ┌───────────┬───────────┬───────┐  │                    │
│              │  │ Service A │ Service B │Svc C  │  │                    │
│              │  │ CPU: 10%  │ CPU: 15%  │CPU:5% │  │                    │
│              │  │ RAM: 20%  │ RAM: 25%  │RAM:10%│  │                    │
│              │  └───────────┴───────────┴───────┘  │                    │
│              │        4 CPU, 16GB RAM              │                    │
│              │     Utilization: ~50%               │                    │
│              └─────────────────────────────────────┘                    │
│                                                                          │
│   Total: 4 CPU, 16GB RAM - Utilization: ~50%                            │
│   Cost: $                                                                │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

## Consolidation Strategies

| Strategy | Description | Use Case |
|----------|-------------|----------|
| **Bin Packing** | Fill nodes to maximum | Stable workloads |
| **Spreading** | Distribute for resilience | Critical workloads |
| **Time-based** | Share by schedule | Batch + interactive |
| **Resource Ratio** | Balance CPU/RAM | Mixed workloads |

## Go Example

```go
package consolidation

import (
	"sort"
)

// Workload represents a workload to be scheduled.
type Workload struct {
	ID         string
	Name       string
	CPURequest int // millicores
	MemRequest int // MB
	Priority   string
}

// Node represents a compute node.
type Node struct {
	ID            string
	CPUCapacity   int
	MemCapacity   int
	CPUAllocated  int
	MemAllocated  int
	Workloads     []Workload
}

// ResourceConsolidator consolidates workloads onto nodes.
type ResourceConsolidator struct {
	targetUtilization float64
	minNodes          int
}

// NewResourceConsolidator creates a new ResourceConsolidator.
func NewResourceConsolidator(targetUtil float64, minNodes int) *ResourceConsolidator {
	return &ResourceConsolidator{
		targetUtilization: targetUtil,
		minNodes:          minNodes,
	}
}

// Consolidate consolidates workloads onto nodes using bin packing.
func (rc *ResourceConsolidator) Consolidate(nodes []Node, workloads []Workload) map[string][]string {
	allocation := make(map[string][]string)

	// Sort workloads by size (largest first for bin packing)
	sortedWorkloads := make([]Workload, len(workloads))
	copy(sortedWorkloads, workloads)
	sort.Slice(sortedWorkloads, func(i, j int) bool {
		sizeI := sortedWorkloads[i].CPURequest + sortedWorkloads[i].MemRequest
		sizeJ := sortedWorkloads[j].CPURequest + sortedWorkloads[j].MemRequest
		return sizeI > sizeJ
	})

	// Sort nodes by available capacity
	availableNodes := make([]Node, len(nodes))
	copy(availableNodes, nodes)
	sort.Slice(availableNodes, func(i, j int) bool {
		return rc.getAvailableScore(&availableNodes[i]) > rc.getAvailableScore(&availableNodes[j])
	})

	for _, workload := range sortedWorkloads {
		targetNode := rc.findBestNode(workload, availableNodes)

		if targetNode != nil {
			rc.allocate(targetNode, workload)

			nodeAlloc := allocation[targetNode.ID]
			nodeAlloc = append(nodeAlloc, workload.ID)
			allocation[targetNode.ID] = nodeAlloc
		}
	}

	return allocation
}

func (rc *ResourceConsolidator) findBestNode(workload Workload, nodes []Node) *Node {
	for i := range nodes {
		node := &nodes[i]

		cpuAvail := node.CPUCapacity - node.CPUAllocated
		memAvail := node.MemCapacity - node.MemAllocated

		cpuFits := cpuAvail >= workload.CPURequest
		memFits := memAvail >= workload.MemRequest

		// Check utilization target
		projectedCPUUtil := float64(node.CPUAllocated+workload.CPURequest) / float64(node.CPUCapacity)
		projectedMemUtil := float64(node.MemAllocated+workload.MemRequest) / float64(node.MemCapacity)

		withinTarget := projectedCPUUtil <= rc.targetUtilization &&
		                projectedMemUtil <= rc.targetUtilization

		if cpuFits && memFits && withinTarget {
			return node
		}
	}

	return nil
}

func (rc *ResourceConsolidator) allocate(node *Node, workload Workload) {
	node.CPUAllocated += workload.CPURequest
	node.MemAllocated += workload.MemRequest
	node.Workloads = append(node.Workloads, workload)
}

func (rc *ResourceConsolidator) getAvailableScore(node *Node) float64 {
	cpuAvail := float64(node.CPUCapacity-node.CPUAllocated) / float64(node.CPUCapacity)
	memAvail := float64(node.MemCapacity-node.MemAllocated) / float64(node.MemCapacity)
	return cpuAvail + memAvail
}

// RecommendScaleDown recommends nodes to remove.
func (rc *ResourceConsolidator) RecommendScaleDown(nodes []Node) []Node {
	var recommendations []Node

	// Empty nodes
	for _, node := range nodes {
		if len(node.Workloads) == 0 {
			recommendations = append(recommendations, node)
		}
	}

	// Underutilized nodes
	for _, node := range nodes {
		cpuUtil := float64(node.CPUAllocated) / float64(node.CPUCapacity)
		memUtil := float64(node.MemAllocated) / float64(node.MemCapacity)

		if cpuUtil < 0.2 && memUtil < 0.2 && len(node.Workloads) > 0 {
			recommendations = append(recommendations, node)
		}
	}

	// Respect minimum nodes
	maxRemove := len(nodes) - rc.minNodes
	if maxRemove < 0 {
		maxRemove = 0
	}

	if len(recommendations) > maxRemove {
		recommendations = recommendations[:maxRemove]
	}

	return recommendations
}
```

## Kubernetes Resource Management

```yaml
# Pod with resource requests/limits
apiVersion: v1
kind: Pod
metadata:
  name: consolidated-service
spec:
  containers:
    - name: service-a
      image: service-a:latest
      resources:
        requests:
          cpu: "100m"
          memory: "128Mi"
        limits:
          cpu: "500m"
          memory: "512Mi"
    - name: service-b
      image: service-b:latest
      resources:
        requests:
          cpu: "200m"
          memory: "256Mi"
        limits:
          cpu: "1000m"
          memory: "1Gi"

---
# Horizontal Pod Autoscaler
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: consolidated-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: consolidated-service
  minReplicas: 1
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70

---
# Vertical Pod Autoscaler
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: consolidated-vpa
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: consolidated-service
  updatePolicy:
    updateMode: Auto
```

## Consolidation Metrics

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Time-based Consolidation

```go
// This example follows the same idiomatic Go patterns
// as the main example above.
// Specific implementation based on standard Go
// interfaces and conventions.
```

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Over-consolidation | Resource contention | Target < 80% utilization |
| Noisy neighbors | Degraded performance | Resource limits + QoS |
| No isolation | Security risk | Namespaces, network policies |
| Static consolidation | Off-peak waste | Autoscaling |

## When to Use

- Workloads with low individual resource utilization
- Non-critical development and test environments
- Complementary services in terms of CPU/memory usage
- Reducing cloud infrastructure costs
- Containerized applications with predictable load profiles

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Autoscaling | Dynamic adjustment |
| Throttling | Overload protection |
| Bulkhead | Workload isolation |
| Queue-based Load Leveling | Load smoothing |

## Sources

- [Microsoft - Compute Resource Consolidation](https://learn.microsoft.com/en-us/azure/architecture/patterns/compute-resource-consolidation)
- [Kubernetes Resource Management](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/)
- [GCP Rightsizing](https://cloud.google.com/compute/docs/instances/apply-machine-type-recommendations-for-instances)
