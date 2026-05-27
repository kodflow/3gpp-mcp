# Branch by Abstraction

Pattern for progressively replacing one implementation with another without long-lived Git branches.

---

## What is Branch by Abstraction?

> Refactoring technique that allows making major changes on trunk/main incrementally and safely.

```
┌─────────────────────────────────────────────────────────────┐
│                    Branch by Abstraction                     │
│                                                              │
│  1. Create abstraction    2. Migrate clients    3. Remove   │
│                                                              │
│  ┌─────┐                 ┌─────┐              ┌─────┐       │
│  │Old  │ ──abstract──►   │Old  │    ──►       │     │       │
│  │Impl │                 │Impl │              │New  │       │
│  └─────┘                 └──┬──┘              │Impl │       │
│                             │                 └─────┘       │
│                          ┌──┴──┐                            │
│                          │New  │                            │
│                          │Impl │                            │
│                          └─────┘                            │
└─────────────────────────────────────────────────────────────┘
```

**Why:**

- Avoid long-lived Git branches (merge hell)
- Deploy continuously on main
- Easy rollback at any time
- Parallel work possible

---

## The Problem: Long-lived Feature Branches

```
❌ BAD - Feature branch for months

main:     A──B──C──D──E──F──G──H──I──J──K──L──M──N──O
               \                                  /
feature:        X──Y──Z──W──V──U──T──S──R──Q──P──┘

Problems:
- Huge merge conflicts
- Delayed integration
- Late integration tests
- Massive code review
```

```
✅ GOOD - Branch by Abstraction

main:     A──B──C──D──E──F──G──H──I──J──K──L──M
              │  │  │  │  │  │  │  │  │  │  │
              │  └──┴──┴──┴──┴──┴──┴──┴──┴──┘
              │     Small progressive commits
              │
              └── Abstraction created
```

---

## Pattern Steps

### Step 1: Create the abstraction

```go
// BEFORE - Direct coupling
type OrderService struct {
	paymentProcessor *StripeProcessor
}

func (s *OrderService) ProcessPayment(ctx context.Context, order *Order) (*PaymentResult, error) {
	return s.paymentProcessor.Charge(ctx, order.Total)
}

// AFTER Step 1 - Interface created
type PaymentProcessor interface {
	Charge(ctx context.Context, amount Money) (*PaymentResult, error)
	Refund(ctx context.Context, transactionID string) error
}

// The old implementation implements the interface
type StripeProcessor struct {
	client *stripe.Client
}

func (p *StripeProcessor) Charge(ctx context.Context, amount Money) (*PaymentResult, error) {
	// existing code
	return &PaymentResult{}, nil
}

func (p *StripeProcessor) Refund(ctx context.Context, transactionID string) error {
	// existing code
	return nil
}

// Service with dependency injection
type OrderService struct {
	processor PaymentProcessor
}

func NewOrderService(processor PaymentProcessor) *OrderService {
	return &OrderService{
		processor: processor,
	}
}

func (s *OrderService) ProcessPayment(ctx context.Context, order *Order) (*PaymentResult, error) {
	return s.processor.Charge(ctx, order.Total)
}
```

**Commit 1:** "Add PaymentProcessor interface" (no functional change)

---

### Step 2: Create the new implementation

```go
// New implementation (may be incomplete)
type AdyenProcessor struct {
	client *adyen.Client
}

func NewAdyenProcessor(client *adyen.Client) *AdyenProcessor {
	return &AdyenProcessor{
		client: client,
	}
}

func (p *AdyenProcessor) Charge(ctx context.Context, amount Money) (*PaymentResult, error) {
	// New implementation
	result, err := p.client.AuthorizePayment(ctx, &adyen.PaymentRequest{
		Amount:   amount.Cents,
		Currency: amount.Currency,
	})
	if err != nil {
		return nil, fmt.Errorf("adyen charge: %w", err)
	}
	return &PaymentResult{
		TransactionID: result.ID,
		Status:        result.Status,
	}, nil
}

func (p *AdyenProcessor) Refund(ctx context.Context, transactionID string) error {
	// TODO: implement
	return fmt.Errorf("refund not implemented yet")
}
```

**Commit 2:** "Add AdyenProcessor implementation (WIP)"

---

### Step 3: Route to the new implementation

```go
// Feature toggle for routing
type PaymentProcessorFactory struct {
	features FeatureFlags
}

func NewPaymentProcessorFactory(features FeatureFlags) *PaymentProcessorFactory {
	return &PaymentProcessorFactory{
		features: features,
	}
}

func (f *PaymentProcessorFactory) Create(ctx context.Context, context PaymentContext) PaymentProcessor {
	// Progressive toggle
	if f.features.IsEnabled(ctx, "adyen-payments", context) {
		return NewAdyenProcessor(adyen.NewClient())
	}
	return NewStripeProcessor(stripe.NewClient())
}

// Or per-method migration
type HybridProcessor struct {
	legacy   *StripeProcessor
	modern   *AdyenProcessor
	features FeatureFlags
}

func NewHybridProcessor(legacy *StripeProcessor, modern *AdyenProcessor, features FeatureFlags) *HybridProcessor {
	return &HybridProcessor{
		legacy:   legacy,
		modern:   modern,
		features: features,
	}
}

func (p *HybridProcessor) Charge(ctx context.Context, amount Money) (*PaymentResult, error) {
	// New implementation for charge
	if p.features.IsEnabled(ctx, "adyen-charge", nil) {
		return p.modern.Charge(ctx, amount)
	}
	return p.legacy.Charge(ctx, amount)
}

func (p *HybridProcessor) Refund(ctx context.Context, transactionID string) error {
	// Still the old one for refund
	return p.legacy.Refund(ctx, transactionID)
}
```

**Commit 3:** "Add feature toggle for AdyenProcessor"
**Commit 4:** "Enable Adyen for 1% of traffic"
**Commit 5:** "Enable Adyen for 10% of traffic"
...
**Commit N:** "Enable Adyen for 100% of traffic"

---

### Step 4: Remove the old implementation

```go
// Once the migration is complete and stable

// Remove:
// - StripeProcessor struct
// - Feature toggles
// - Routing code

// Keep:
// - PaymentProcessor interface (for future migrations)
// - AdyenProcessor (now the only implementation)
```

**Final commit:** "Remove StripeProcessor (migration complete)"

---

## Variants

### Strangler Fig Pattern

> Progressively strangle the old system.

```go
// To migrate a monolith to microservices

type OrderFacade struct {
	legacyService *LegacyOrderService
	newService    *OrderMicroservice
	features      FeatureFlags
}

func NewOrderFacade(
	legacy *LegacyOrderService,
	modern *OrderMicroservice,
	features FeatureFlags,
) *OrderFacade {
	return &OrderFacade{
		legacyService: legacy,
		newService:    modern,
		features:      features,
	}
}

func (f *OrderFacade) CreateOrder(ctx context.Context, data *OrderData) (*Order, error) {
	// Route to the new service progressively
	if f.shouldUseNewService(ctx, data) {
		return f.newService.Create(ctx, data)
	}
	return f.legacyService.Create(ctx, data)
}

func (f *OrderFacade) shouldUseNewService(ctx context.Context, data *OrderData) bool {
	// Migration criteria
	return data.Region == "EU" && // Europe first
		data.Total.Amount < 10000 && // Small orders
		f.features.IsEnabled(ctx, "new-order-service", data)
}
```

### Parallel Run

> Run both implementations and compare.

```go
type ParallelPaymentProcessor struct {
	primary    PaymentProcessor
	shadow     PaymentProcessor
	comparator ResultComparator
	logger     *slog.Logger
}

func NewParallelPaymentProcessor(
	primary PaymentProcessor,
	shadow PaymentProcessor,
	comparator ResultComparator,
	logger *slog.Logger,
) *ParallelPaymentProcessor {
	return &ParallelPaymentProcessor{
		primary:    primary,
		shadow:     shadow,
		comparator: comparator,
		logger:     logger,
	}
}

func (p *ParallelPaymentProcessor) Charge(ctx context.Context, amount Money) (*PaymentResult, error) {
	type result struct {
		val *PaymentResult
		err error
	}

	// Channels to receive results
	primaryCh := make(chan result, 1)
	shadowCh := make(chan result, 1)

	// Run in parallel
	go func() {
		val, err := p.primary.Charge(ctx, amount)
		primaryCh <- result{val: val, err: err}
	}()

	go func() {
		val, err := p.shadow.Charge(ctx, amount)
		shadowCh <- result{val: val, err: err}
	}()

	// Wait for results
	primaryResult := <-primaryCh
	shadowResult := <-shadowCh

	// Compare (async, non-blocking)
	go func() {
		if err := p.comparator.Compare(ctx, primaryResult, shadowResult); err != nil {
			p.logger.WarnContext(ctx, "Shadow comparison failed", "error", err)
		}
	}()

	// Return only the primary result
	if primaryResult.err != nil {
		return nil, primaryResult.err
	}
	return primaryResult.val, nil
}

func (p *ParallelPaymentProcessor) Refund(ctx context.Context, transactionID string) error {
	return p.primary.Refund(ctx, transactionID)
}
```

### Dark Launch

> New implementation activated but result ignored.

```go
type DarkLaunchProcessor struct {
	legacy  PaymentProcessor
	modern  PaymentProcessor
	metrics MetricsRecorder
	logger  *slog.Logger
}

func NewDarkLaunchProcessor(
	legacy PaymentProcessor,
	modern PaymentProcessor,
	metrics MetricsRecorder,
	logger *slog.Logger,
) *DarkLaunchProcessor {
	return &DarkLaunchProcessor{
		legacy:  legacy,
		modern:  modern,
		metrics: metrics,
		logger:  logger,
	}
}

func (p *DarkLaunchProcessor) Charge(ctx context.Context, amount Money) (*PaymentResult, error) {
	// Always use legacy for the actual result
	result, err := p.legacy.Charge(ctx, amount)

	// Test the new one in the background
	go func() {
		modernResult, modernErr := p.modern.Charge(ctx, amount)
		if modernErr != nil {
			p.metrics.Record(ctx, "dark-launch-failure", 1)
			p.logger.ErrorContext(ctx, "Dark launch error", "error", modernErr)
			return
		}

		p.metrics.Record(ctx, "dark-launch-success", 1)
		if !p.resultsMatch(result, modernResult) {
			p.logger.WarnContext(ctx, "Dark launch mismatch",
				"legacy", result,
				"modern", modernResult)
		}
	}()

	return result, err
}

func (p *DarkLaunchProcessor) Refund(ctx context.Context, transactionID string) error {
	return p.legacy.Refund(ctx, transactionID)
}

func (p *DarkLaunchProcessor) resultsMatch(a, b *PaymentResult) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.TransactionID == b.TransactionID && a.Status == b.Status
}
```

---

## Complete Example: Database Migration

```go
// Migration from MySQL to PostgreSQL

// Step 1: Abstraction
type UserRepository interface {
	FindByID(ctx context.Context, id string) (*User, error)
	Save(ctx context.Context, user *User) error
	FindByEmail(ctx context.Context, email string) (*User, error)
}

// Step 2: Implementations
type MySQLUserRepository struct {
	db *sql.DB
}

func NewMySQLUserRepository(db *sql.DB) *MySQLUserRepository {
	return &MySQLUserRepository{db: db}
}

func (r *MySQLUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	// Existing MySQL implementation
	var user User
	err := r.db.QueryRowContext(ctx, "SELECT * FROM users WHERE id = ?", id).Scan(&user)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("mysql find by id: %w", err)
	}
	return &user, nil
}

func (r *MySQLUserRepository) Save(ctx context.Context, user *User) error {
	_, err := r.db.ExecContext(ctx, "INSERT INTO users (...) VALUES (?)", user)
	if err != nil {
		return fmt.Errorf("mysql save: %w", err)
	}
	return nil
}

func (r *MySQLUserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	// Implementation
	return nil, nil
}

type PostgresUserRepository struct {
	db *sql.DB
}

func NewPostgresUserRepository(db *sql.DB) *PostgresUserRepository {
	return &PostgresUserRepository{db: db}
}

func (r *PostgresUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	// New Postgres implementation
	var user User
	err := r.db.QueryRowContext(ctx, "SELECT * FROM users WHERE id = $1", id).Scan(&user)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres find by id: %w", err)
	}
	return &user, nil
}

func (r *PostgresUserRepository) Save(ctx context.Context, user *User) error {
	_, err := r.db.ExecContext(ctx, "INSERT INTO users (...) VALUES ($1)", user)
	if err != nil {
		return fmt.Errorf("postgres save: %w", err)
	}
	return nil
}

func (r *PostgresUserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	// Implementation
	return nil, nil
}

// Step 3: Double-write for migration
type MigratingUserRepository struct {
	mysql          *MySQLUserRepository
	postgres       *PostgresUserRepository
	migrationState *MigrationState
	logger         *slog.Logger
}

func NewMigratingUserRepository(
	mysql *MySQLUserRepository,
	postgres *PostgresUserRepository,
	state *MigrationState,
	logger *slog.Logger,
) *MigratingUserRepository {
	return &MigratingUserRepository{
		mysql:          mysql,
		postgres:       postgres,
		migrationState: state,
		logger:         logger,
	}
}

func (r *MigratingUserRepository) Save(ctx context.Context, user *User) error {
	// Write to both
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Go(func() {
		if err := r.mysql.Save(ctx, user); err != nil {
			errCh <- fmt.Errorf("mysql save: %w", err)
		}
	})

	wg.Go(func() {
		if err := r.postgres.Save(ctx, user); err != nil {
			errCh <- fmt.Errorf("postgres save: %w", err)
		}
	})

	wg.Wait()
	close(errCh)

	// Return the first error if present
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *MigratingUserRepository) FindByID(ctx context.Context, id string) (*User, error) {
	// Read from primary based on migration state
	if r.migrationState.IsComplete() {
		return r.postgres.FindByID(ctx, id)
	}

	// During migration: read from MySQL, verify Postgres
	mysqlUser, err := r.mysql.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("mysql find: %w", err)
	}

	postgresUser, err := r.postgres.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("postgres find: %w", err)
	}

	if !r.usersMatch(mysqlUser, postgresUser) {
		r.logger.WarnContext(ctx, "Data mismatch during migration", "id", id)
		// Self-heal: copy from MySQL to Postgres
		if mysqlUser != nil {
			if err := r.postgres.Save(ctx, mysqlUser); err != nil {
				r.logger.ErrorContext(ctx, "Failed to heal data", "error", err)
			}
		}
	}

	return mysqlUser, nil // MySQL remains primary during migration
}

func (r *MigratingUserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	// Similar to FindByID
	return nil, nil
}

func (r *MigratingUserRepository) usersMatch(a, b *User) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.Email == b.Email
}

// Step 4: Progressive cutover
type MigrationState struct {
	mu               sync.RWMutex
	readFromPostgres int // 0-100%
}

func NewMigrationState() *MigrationState {
	return &MigrationState{
		readFromPostgres: 0,
	}
}

func (m *MigrationState) IsComplete() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.readFromPostgres == 100
}

func (m *MigrationState) ShouldReadFromPostgres(userID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Canary based on userID hash
	hash := m.hashCode(userID)
	return (hash % 100) < m.readFromPostgres
}

func (m *MigrationState) IncrementPercentage(increment int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.readFromPostgres = min(100, m.readFromPostgres+increment)
	return m.persist()
}

func (m *MigrationState) hashCode(s string) int {
	h := 0
	for _, c := range s {
		h = 31*h + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h
}

func (m *MigrationState) persist() error {
	// Persist state in config
	return nil
}
```

---

## Decision Table

| Situation | Approach |
|-----------|----------|
| Simple internal refactoring | Git branch + PR |
| API/Service migration | Branch by Abstraction |
| Database migration | Double-write + Parallel Run |
| Dependency replacement | Strangler Fig |
| Testing new implementation | Dark Launch |
| Progressive rollout | Feature Toggle + Canary |

---

## Related Patterns

| Pattern | Relationship |
|---------|--------------|
| **Feature Toggles** | Routing mechanism |
| **Adapter** | Common interface |
| **Strategy** | Interchangeability |
| **Strangler Fig** | Variant for legacy |
| **Parallel Run** | Migration validation |

---

## Advantages vs Disadvantages

### Advantages

- Continuous integration (no merge hell)
- Instant rollback (toggle off)
- Incremental code reviews
- Continuous integration tests
- Deployment at any time

### Disadvantages

- Temporarily more complex code
- Toggle debt if not cleaned up
- Requires team discipline
- More complex monitoring

---

## Sources

- [Martin Fowler - Branch by Abstraction](https://martinfowler.com/bliki/BranchByAbstraction.html)
- [Paul Hammant - Trunk Based Development](https://trunkbaseddevelopment.com/branch-by-abstraction/)
- [Strangler Fig Application](https://martinfowler.com/bliki/StranglerFigApplication.html)
