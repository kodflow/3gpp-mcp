# Serverless Architecture

> Architecture where infrastructure is managed by the cloud provider, billed per usage.

**Also called:** FaaS (Function as a Service), Event-driven Serverless

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                   SERVERLESS ARCHITECTURE                        │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                      EVENT SOURCES                       │    │
│  │                                                          │    │
│  │  ┌──────┐  ┌──────┐  ┌──────┐  ┌──────┐  ┌──────┐      │    │
│  │  │ HTTP │  │ Queue│  │  S3  │  │ Cron │  │Stream│      │    │
│  │  │  API │  │ SQS  │  │Event │  │      │  │Kinesis│     │    │
│  │  └──┬───┘  └──┬───┘  └──┬───┘  └──┬───┘  └──┬───┘      │    │
│  └─────┼─────────┼────────┼────────┼─────────┼───────────┘    │
│        │         │        │        │         │                  │
│        ▼         ▼        ▼        ▼         ▼                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    FUNCTIONS (Lambda)                    │    │
│  │                                                          │    │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐              │    │
│  │  │ getUser  │  │processOrder │ │resizeImage│            │    │
│  │  │          │  │          │  │          │              │    │
│  │  │  Auto-   │  │  Auto-   │  │  Auto-   │              │    │
│  │  │  scale   │  │  scale   │  │  scale   │              │    │
│  │  └──────────┘  └──────────┘  └──────────┘              │    │
│  └─────────────────────────────────────────────────────────┘    │
│        │                   │              │                      │
│        ▼                   ▼              ▼                      │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    MANAGED SERVICES                      │    │
│  │  ┌────────┐  ┌────────┐  ┌────────┐  ┌────────┐        │    │
│  │  │DynamoDB│  │   S3   │  │  SQS   │  │Cognito │        │    │
│  │  └────────┘  └────────┘  └────────┘  └────────┘        │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                  │
│  No servers to manage       Pay per invocation                  │
│  No scaling config          Auto-scale to zero                  │
└─────────────────────────────────────────────────────────────────┘
```

## Typical AWS Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     API + COMPUTE + DATA                         │
│                                                                  │
│  Client                                                          │
│    │                                                             │
│    ▼                                                             │
│  ┌──────────────┐                                               │
│  │ API Gateway  │  (REST/HTTP/WebSocket)                        │
│  └──────┬───────┘                                               │
│         │                                                        │
│         ▼                                                        │
│  ┌──────────────┐     ┌──────────────┐                          │
│  │    Lambda    │────►│   DynamoDB   │                          │
│  │   (handler)  │     │   (NoSQL)    │                          │
│  └──────────────┘     └──────────────┘                          │
│         │                                                        │
│         │ async                                                  │
│         ▼                                                        │
│  ┌──────────────┐     ┌──────────────┐                          │
│  │ EventBridge  │────►│    Lambda    │                          │
│  │   (events)   │     │  (processor) │                          │
│  └──────────────┘     └──────────────┘                          │
│                              │                                   │
│                              ▼                                   │
│                       ┌──────────────┐                          │
│                       │     SES      │                          │
│                       │   (email)    │                          │
│                       └──────────────┘                          │
└─────────────────────────────────────────────────────────────────┘
```

## AWS Lambda Implementation

### Basic Handler

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// UserResponse is the response for user requests.
type UserResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// ErrorResponse is the error response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// GetUser handles the get user Lambda function.
func GetUser(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	userID := request.PathParameters["id"]

	if userID == "" {
		return events.APIGatewayProxyResponse{
			StatusCode: 400,
			Body:       `{"error":"Missing user ID"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	user, err := userService.GetByID(ctx, userID)
	if err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: 500,
			Body:       fmt.Sprintf(`{"error":"%s"}`, err.Error()),
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	if user == nil {
		return events.APIGatewayProxyResponse{
			StatusCode: 404,
			Body:       `{"error":"User not found"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	response := UserResponse{
		ID:    user.ID,
		Email: user.Email,
		Name:  user.Name,
	}

	body, err := json.Marshal(response)
	if err != nil {
		return events.APIGatewayProxyResponse{
			StatusCode: 500,
			Body:       `{"error":"Internal server error"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       string(body),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}, nil
}

func main() {
	lambda.Start(GetUser)
}
```

### Event-driven Handler

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// Order represents an order.
type Order struct {
	ID     string
	UserID string
	Total  float64
}

// ProcessOrder handles SQS events for order processing.
func ProcessOrder(ctx context.Context, sqsEvent events.SQSEvent) error {
	for _, record := range sqsEvent.Records {
		var order Order
		if err := json.Unmarshal([]byte(record.Body), &order); err != nil {
			return fmt.Errorf("unmarshaling order: %w", err)
		}

		// Process order
		if err := orderService.Process(ctx, &order); err != nil {
			return fmt.Errorf("processing order: %w", err)
		}

		// Emit event for other functions
		event := map[string]interface{}{
			"Source":     "orders",
			"DetailType": "OrderProcessed",
			"Detail": map[string]interface{}{
				"orderId": order.ID,
			},
		}

		if err := eventBridge.PutEvents(ctx, event); err != nil {
			return fmt.Errorf("publishing event: %w", err)
		}
	}

	return nil
}

func main() {
	lambda.Start(ProcessOrder)
}
```

## Infrastructure as Code (SAM)

```yaml
# template.yaml
AWSTemplateFormatVersion: '2010-09-09'
Transform: AWS::Serverless-2016-10-31

Globals:
  Function:
    Runtime: provided.al2023  # Go custom runtime
    Timeout: 30
    MemorySize: 256
    Environment:
      Variables:
        TABLE_NAME: !Ref UsersTable

Resources:
  # API Gateway
  ApiGateway:
    Type: AWS::Serverless::Api
    Properties:
      StageName: prod
      Cors:
        AllowOrigin: "'*'"

  # Lambda Functions
  GetUserFunction:
    Type: AWS::Serverless::Function
    Properties:
      Handler: bootstrap  # Go uses bootstrap
      CodeUri: ./build/getUser.zip
      Events:
        GetUser:
          Type: Api
          Properties:
            RestApiId: !Ref ApiGateway
            Path: /users/{id}
            Method: GET
      Policies:
        - DynamoDBReadPolicy:
            TableName: !Ref UsersTable

  CreateUserFunction:
    Type: AWS::Serverless::Function
    Properties:
      Handler: bootstrap
      CodeUri: ./build/createUser.zip
      Events:
        CreateUser:
          Type: Api
          Properties:
            RestApiId: !Ref ApiGateway
            Path: /users
            Method: POST
      Policies:
        - DynamoDBCrudPolicy:
            TableName: !Ref UsersTable

  # DynamoDB Table
  UsersTable:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: users
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions:
        - AttributeName: id
          AttributeType: S
      KeySchema:
        - AttributeName: id
          KeyType: HASH
```

## Serverless Patterns

### API Pattern

```
Client -> API Gateway -> Lambda -> DynamoDB
```

### Fan-out Pattern

```
                    ┌─── Lambda A -> Service A
Event -> Lambda ────┼─── Lambda B -> Service B
                    └─── Lambda C -> Service C
```

### Saga Pattern (Step Functions)

```yaml
# step-functions.yaml
OrderProcessingSaga:
  Type: AWS::Serverless::StateMachine
  Properties:
    Definition:
      StartAt: ReserveInventory
      States:
        ReserveInventory:
          Type: Task
          Resource: !GetAtt ReserveInventoryFunction.Arn
          Next: ProcessPayment
          Catch:
            - ErrorEquals: ["InventoryError"]
              Next: ReleaseInventory

        ProcessPayment:
          Type: Task
          Resource: !GetAtt ProcessPaymentFunction.Arn
          Next: CompleteOrder
          Catch:
            - ErrorEquals: ["PaymentError"]
              Next: ReleaseInventory

        ReleaseInventory:
          Type: Task
          Resource: !GetAtt ReleaseInventoryFunction.Arn
          Next: Fail

        CompleteOrder:
          Type: Task
          Resource: !GetAtt CompleteOrderFunction.Arn
          End: true

        Fail:
          Type: Fail
```

## When to Use

| Use | Avoid |
|----------|--------|
| Variable traffic | Constant high traffic |
| Simple APIs | Long computations (>15min) |
| Event processing | Stateful applications |
| Startups (low initial cost) | Ultra-low latency required |
| Prototypes | Vendor lock-in problematic |

## Advantages

- **No ops**: No servers to manage
- **Auto-scale**: Automatic scaling (including to 0)
- **Pay-per-use**: Billed per invocation
- **Focus code**: Business logic only
- **High availability**: Built-in
- **Integrations**: Rich cloud ecosystem

## Disadvantages

- **Cold starts**: Startup latency
- **Timeout**: Execution limits (15min max)
- **Vendor lock-in**: Provider dependency
- **Debugging**: More complex
- **Stateless**: External state required
- **Cost**: Can explode at high traffic

## Cold Start Mitigation

```yaml
# Provisioned Concurrency (SAM)
Resources:
  MyFunction:
    Type: AWS::Serverless::Function
    Properties:
      AutoPublishAlias: live
      ProvisionedConcurrencyConfig:
        ProvisionedConcurrentExecutions: 10
```

## Real-world Examples

| Company | Usage |
|------------|-------|
| **Netflix** | Data processing |
| **Coca-Cola** | Vending machines IoT |
| **iRobot** | Robot communications |
| **Nordstrom** | E-commerce backend |
| **Financial Times** | Content delivery |

## Migration Path

### To Serverless

```
Phase 1: Identify event-driven workloads
Phase 2: Containerize functions
Phase 3: Deploy on Lambda/Cloud Functions
Phase 4: Migrate data to managed services
Phase 5: Implement observability
```

### From Serverless (scale out)

```
1. Containerize Lambdas
2. Deploy on ECS/EKS
3. Replace with Fargate/Kubernetes
```

## Related Patterns

| Pattern | Relationship |
|---------|----------|
| Event-Driven | Underlying architecture |
| CQRS | Separate read/write |
| Saga | Distributed transactions |
| Circuit Breaker | Resilience |

## Sources

- [AWS Lambda](https://aws.amazon.com/lambda/)
- [Serverless Framework](https://www.serverless.com/)
- [AWS SAM](https://aws.amazon.com/serverless/sam/)
- [Martin Fowler - Serverless](https://martinfowler.com/articles/serverless.html)
