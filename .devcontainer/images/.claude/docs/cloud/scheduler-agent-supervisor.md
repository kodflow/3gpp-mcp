# Scheduler-Agent-Supervisor Pattern

> Coordinate distributed tasks with a centralized supervisor.

## Principle

```
┌─────────────────────────────────────────────────────────────────────┐
│                         SUPERVISOR                                   │
│                                                                      │
│   ┌─────────────────┐    ┌─────────────────┐    ┌───────────────┐   │
│   │    Scheduler    │    │   State Store   │    │   Recovery    │   │
│   │                 │    │                 │    │   Manager     │   │
│   │ - Scheduling    │    │ - Task state    │    │ - Retry       │   │
│   │ - Priorities    │    │ - History       │    │ - Compensation│   │
│   │ - Timing        │    │ - Checkpoints   │    │ - Alerting    │   │
│   └────────┬────────┘    └─────────────────┘    └───────────────┘   │
│            │                                                         │
└────────────┼─────────────────────────────────────────────────────────┘
             │
             │  Dispatch Tasks
             ▼
     ┌───────────────────────────────────────────────────────────┐
     │                        AGENTS                              │
     │                                                            │
     │   ┌─────────┐    ┌─────────┐    ┌─────────┐    ┌────────┐ │
     │   │ Agent A │    │ Agent B │    │ Agent C │    │Agent N │ │
     │   │(Worker) │    │(Worker) │    │(Worker) │    │(Worker)│ │
     │   └────┬────┘    └────┬────┘    └────┬────┘    └───┬────┘ │
     │        │              │              │              │      │
     │        └──────────────┴──────────────┴──────────────┘      │
     │                           │                                 │
     │                  Report Status                              │
     └───────────────────────────────────────────────────────────┘
```

## Components

| Component | Responsibility |
|-----------|----------------|
| **Scheduler** | Plans and assigns tasks |
| **Agent** | Executes atomic tasks |
| **Supervisor** | Monitors, recovers from failures |
| **State Store** | Persists task state |

## Go Example

```go
package schedulerAgentSupervisor

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Task represents a task to be executed.
type Task struct {
	ID            string
	Type          string
	Payload       interface{}
	Status        string // pending, running, completed, failed
	Retries       int
	MaxRetries    int
	AssignedAgent string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Agent represents a worker agent.
type Agent struct {
	ID           string
	Status       string // idle, busy, offline
	Capabilities []string
	LastHeartbeat time.Time
}

// TaskStore defines task storage operations.
type TaskStore interface {
	FindByStatus(ctx context.Context, status string) ([]Task, error)
	Update(ctx context.Context, task *Task) error
	SaveResult(ctx context.Context, taskID string, result interface{}) error
	SaveError(ctx context.Context, taskID string, errMsg string) error
}

// AgentRegistry manages agent registrations.
type AgentRegistry interface{
	FindByStatus(ctx context.Context, status string) ([]Agent, error)
	Find(ctx context.Context, agentID string) (*Agent, error)
	Update(ctx context.Context, agent *Agent) error
	FindAll(ctx context.Context) ([]Agent, error)
}

// Scheduler schedules tasks to agents.
type Scheduler struct {
	taskStore     TaskStore
	agentRegistry AgentRegistry
}

// NewScheduler creates a new Scheduler.
func NewScheduler(taskStore TaskStore, agentRegistry AgentRegistry) *Scheduler {
	return &Scheduler{
		taskStore:     taskStore,
		agentRegistry: agentRegistry,
	}
}

// ScheduleTasks assigns pending tasks to idle agents.
func (s *Scheduler) ScheduleTasks(ctx context.Context) error {
	pendingTasks, err := s.taskStore.FindByStatus(ctx, "pending")
	if err != nil {
		return fmt.Errorf("finding pending tasks: %w", err)
	}

	idleAgents, err := s.agentRegistry.FindByStatus(ctx, "idle")
	if err != nil {
		return fmt.Errorf("finding idle agents: %w", err)
	}

	for _, task := range pendingTasks {
		if len(idleAgents) == 0 {
			break
		}

		agent := s.findCapableAgent(&task, idleAgents)
		if agent != nil {
			if err := s.assignTask(ctx, &task, agent); err != nil {
				log.Printf("Failed to assign task %s: %v", task.ID, err)
				continue
			}

			// Remove agent from idle list
			for i, a := range idleAgents {
				if a.ID == agent.ID {
					idleAgents = append(idleAgents[:i], idleAgents[i+1:]...)
					break
				}
			}
		}
	}

	return nil
}

func (s *Scheduler) findCapableAgent(task *Task, agents []Agent) *Agent {
	for i := range agents {
		for _, capability := range agents[i].Capabilities {
			if capability == task.Type {
				return &agents[i]
			}
		}
	}
	return nil
}

func (s *Scheduler) assignTask(ctx context.Context, task *Task, agent *Agent) error {
	task.Status = "running"
	task.AssignedAgent = agent.ID
	task.UpdatedAt = time.Now()

	if err := s.taskStore.Update(ctx, task); err != nil {
		return fmt.Errorf("updating task: %w", err)
	}

	// Notify agent (simplified - would use HTTP or message queue)
	log.Printf("Assigned task %s to agent %s", task.ID, agent.ID)

	return nil
}

// Supervisor monitors and recovers from failures.
type Supervisor struct {
	taskStore     TaskStore
	agentRegistry AgentRegistry
	alertService  AlertService
	checkInterval time.Duration
}

// AlertService handles alerts.
type AlertService interface {
	Notify(ctx context.Context, message string) error
}

// NewSupervisor creates a new Supervisor.
func NewSupervisor(
	taskStore TaskStore,
	agentRegistry AgentRegistry,
	alertService AlertService,
	checkInterval time.Duration,
) *Supervisor {
	return &Supervisor{
		taskStore:     taskStore,
		agentRegistry: agentRegistry,
		alertService:  alertService,
		checkInterval: checkInterval,
	}
}

// Start starts the supervisor.
func (sv *Supervisor) Start(ctx context.Context) {
	ticker := time.NewTicker(sv.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sv.checkHealth(ctx)
		}
	}
}

func (sv *Supervisor) checkHealth(ctx context.Context) {
	sv.detectStaleAgents(ctx)
	sv.recoverStaleTasks(ctx)
	sv.retryFailedTasks(ctx)
}

func (sv *Supervisor) detectStaleAgents(ctx context.Context) {
	agents, err := sv.agentRegistry.FindAll(ctx)
	if err != nil {
		log.Printf("Failed to find agents: %v", err)
		return
	}

	now := time.Now()
	for _, agent := range agents {
		if now.Sub(agent.LastHeartbeat) > time.Minute {
			agent.Status = "offline"
			sv.agentRegistry.Update(ctx, &agent)
			sv.alertService.Notify(ctx, fmt.Sprintf("Agent %s is offline", agent.ID))
		}
	}
}

func (sv *Supervisor) recoverStaleTasks(ctx context.Context) {
	runningTasks, err := sv.taskStore.FindByStatus(ctx, "running")
	if err != nil {
		log.Printf("Failed to find running tasks: %v", err)
		return
	}

	for _, task := range runningTasks {
		agent, err := sv.agentRegistry.Find(ctx, task.AssignedAgent)
		if err != nil || agent == nil || agent.Status == "offline" {
			task.Status = "pending"
			task.AssignedAgent = ""
			sv.taskStore.Update(ctx, &task)
		}
	}
}

func (sv *Supervisor) retryFailedTasks(ctx context.Context) {
	failedTasks, err := sv.taskStore.FindByStatus(ctx, "failed")
	if err != nil {
		log.Printf("Failed to find failed tasks: %v", err)
		return
	}

	for _, task := range failedTasks {
		if task.Retries < task.MaxRetries {
			task.Status = "pending"
			task.Retries++
			sv.taskStore.Update(ctx, &task)
		} else {
			sv.alertService.Notify(ctx, fmt.Sprintf("Task %s exceeded max retries", task.ID))
		}
	}
}
```

## Complete Workflow

```
1. Client submits a task
          │
          ▼
2. Scheduler places in queue
          │
          ▼
3. Scheduler assigns to an idle Agent
          │
          ▼
4. Agent executes the task
          │
    ┌─────┴─────┐
    ▼           ▼
Success      Failure
    │           │
    ▼           ▼
5a. Report   5b. Report
 completion   failure
    │           │
    └─────┬─────┘
          ▼
6. Supervisor updates the state
          │
          ▼
7. Supervisor retries if necessary
```

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| Supervisor SPOF | Failure = no recovery | High availability supervisor |
| Excessive polling | Network load | Long polling / events |
| Non-idempotent tasks | Retries cause duplications | Idempotent design |
| Without timeout | Zombie tasks | Timeout + detection |

## When to Use

- Complex workflows with multiple dependent steps
- Distributed tasks requiring centralized coordination
- Batch processing systems with monitoring and recovery
- Processing pipelines with automatic retry
- Job orchestration in a worker cluster

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Saga | Distributed transactions |
| Queue-based Load Leveling | Task buffer |
| Competing Consumers | Multiple agents |
| Leader Election | Supervisor HA |

## Sources

- [Microsoft - Scheduler Agent Supervisor](https://learn.microsoft.com/en-us/azure/architecture/patterns/scheduler-agent-supervisor)
- [Temporal.io](https://temporal.io/)
