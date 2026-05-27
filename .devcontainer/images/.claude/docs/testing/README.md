# Testing Patterns

Patterns for automated testing.

## Test Doubles

### 1. Mock

> Object that verifies interactions.

```go
// Manual mock
class MockEmailService implements EmailService {
  private calls: Array<{ to: string; subject: string; body: string }> = [];

  async send(to: string, subject: string, body: string): Promise<void> {
    this.calls.push({ to, subject, body });
  }

  // Verification methods
  wasCalled(): boolean {
    return this.calls.length > 0;
  }

  wasCalledWith(to: string, subject: string): boolean {
    return this.calls.some((c) => c.to === to && c.subject === subject);
  }

  getCallCount(): number {
    return this.calls.length;
  }

  getLastCall() {
    return this.calls[this.calls.length - 1];
  }
}

// With Jest
const mockEmailService = {
  send: jest.fn().mockResolvedValue(undefined),
};

// Usage in test
test('should send welcome email', async () => {
  const service = new UserService(mockEmailService);
  await service.register({ email: 'user@example.com' });

  expect(mockEmailService.send).toHaveBeenCalledWith(
    'user@example.com',
    'Welcome!',
    expect.stringContaining('Thank you'),
  );
  expect(mockEmailService.send).toHaveBeenCalledTimes(1);
});
```

**When:** Verify behavior, interactions, calls.
**Related:** Spy.

---

### 2. Stub

> Object that returns predefined values.

```go
// Stub with fixed response
class StubUserRepository implements UserRepository {
  private users: User[] = [];

  setUsers(users: User[]) {
    this.users = users;
  }

  async findById(id: string): Promise<User | null> {
    return this.users.find((u) => u.id === id) || null;
  }

  async findByEmail(email: string): Promise<User | null> {
    return this.users.find((u) => u.email === email) || null;
  }

  async save(user: User): Promise<void> {
    this.users.push(user);
  }
}

// Usage
test('should return user profile', async () => {
  const userRepo = new StubUserRepository();
  userRepo.setUsers([
    { id: '1', name: 'John', email: 'john@example.com' },
  ]);

  const service = new ProfileService(userRepo);
  const profile = await service.getProfile('1');

  expect(profile.name).toBe('John');
});

// Jest stub
jest.spyOn(userRepo, 'findById').mockResolvedValue({
  id: '1',
  name: 'John',
  email: 'john@example.com',
});
```

**When:** Control returned data, specific scenarios.
**Related:** Mock, Fake.

---

### 3. Fake

> Simplified functional implementation.

```go
// In-memory database fake
class FakeUserRepository implements UserRepository {
  private users = new Map<string, User>();

  async findById(id: string): Promise<User | null> {
    return this.users.get(id) || null;
  }

  async findByEmail(email: string): Promise<User | null> {
    for (const user of this.users.values()) {
      if (user.email === email) return user;
    }
    return null;
  }

  async save(user: User): Promise<void> {
    this.users.set(user.id, { ...user });
  }

  async delete(id: string): Promise<void> {
    this.users.delete(id);
  }

  async findAll(): Promise<User[]> {
    return [...this.users.values()];
  }

  // Test helper
  clear() {
    this.users.clear();
  }
}

// Fake HTTP client
class FakeHttpClient implements HttpClient {
  private responses = new Map<string, any>();

  setResponse(url: string, response: any) {
    this.responses.set(url, response);
  }

  async get<T>(url: string): Promise<T> {
    const response = this.responses.get(url);
    if (!response) {
      throw new Error(`No fake response for ${url}`);
    }
    return response;
  }
}
```

**When:** Integration tests, realistic behavior.
**Related:** Stub.

---

### 4. Spy

> Wrapper that records calls.

```go
// Manual spy
function createSpy<T extends (...args: any[]) => any>(fn: T): T & {
  calls: Array<{ args: Parameters<T>; result: ReturnType<T> }>;
  callCount: number;
} {
  const calls: Array<{ args: Parameters<T>; result: ReturnType<T> }> = [];

  const spy = ((...args: Parameters<T>) => {
    const result = fn(...args);
    calls.push({ args, result });
    return result;
  }) as T & {
    calls: Array<{ args: Parameters<T>; result: ReturnType<T> }>;
    callCount: number;
  };

  Object.defineProperty(spy, 'calls', { get: () => calls });
  Object.defineProperty(spy, 'callCount', { get: () => calls.length });

  return spy;
}

// Usage
const calculator = {
  add: (a: number, b: number) => a + b,
};

const spiedAdd = createSpy(calculator.add);
calculator.add = spiedAdd;

calculator.add(1, 2);
calculator.add(3, 4);

console.log(spiedAdd.callCount); // 2
console.log(spiedAdd.calls[0].args); // [1, 2]

// Jest spy
const spy = jest.spyOn(service, 'process');
await service.doSomething();
expect(spy).toHaveBeenCalled();
```

**When:** Observe without replacing, partial mock.
**Related:** Mock.

---

### 5. Dummy

> Object that fills a parameter without being used.

```go
// Dummy object - never actually used
class DummyLogger implements Logger {
  log(message: string): void {
    // Do nothing
  }

  error(message: string): void {
    // Do nothing
  }

  warn(message: string): void {
    // Do nothing
  }
}

// Usage
test('should process order without logging', () => {
  const dummyLogger = new DummyLogger();
  const service = new OrderService(
    realOrderRepo,
    realPaymentService,
    dummyLogger, // Required but not relevant for this test
  );

  const result = service.process(order);
  expect(result.status).toBe('completed');
});
```

**When:** Required parameters not relevant for the test.

---

## Test Organization

### 6. Arrange-Act-Assert (AAA)

> Clear test structure.

```go
test('should apply discount to order total', () => {
  // Arrange - Setup
  const order = new Order();
  order.addItem(new Product('Widget', 100));
  order.addItem(new Product('Gadget', 50));
  const discountService = new DiscountService();

  // Act - Execute
  const discountedOrder = discountService.applyDiscount(order, 0.1);

  // Assert - Verify
  expect(discountedOrder.total).toBe(135); // 150 - 10%
});

// Async version
test('should fetch user profile', async () => {
  // Arrange
  const userId = '123';
  const mockRepo = new MockUserRepository();
  mockRepo.setUser({ id: userId, name: 'John' });
  const service = new ProfileService(mockRepo);

  // Act
  const profile = await service.getProfile(userId);

  // Assert
  expect(profile).toEqual({
    id: userId,
    name: 'John',
    displayName: 'John',
  });
});
```

**When:** ALWAYS for structuring tests.

---

### 7. Given-When-Then (BDD)

> Behavior-driven style.

```go
describe('Shopping Cart', () => {
  describe('given an empty cart', () => {
    let cart: ShoppingCart;

    beforeEach(() => {
      cart = new ShoppingCart();
    });

    describe('when adding a product', () => {
      beforeEach(() => {
        cart.add(new Product('Widget', 10), 2);
      });

      it('then should have one item', () => {
        expect(cart.items.length).toBe(1);
      });

      it('then should calculate correct total', () => {
        expect(cart.total).toBe(20);
      });
    });
  });

  describe('given a cart with items', () => {
    let cart: ShoppingCart;

    beforeEach(() => {
      cart = new ShoppingCart();
      cart.add(new Product('Widget', 10), 2);
      cart.add(new Product('Gadget', 15), 1);
    });

    describe('when removing an item', () => {
      beforeEach(() => {
        cart.remove('Widget');
      });

      it('then should have one item remaining', () => {
        expect(cart.items.length).toBe(1);
      });

      it('then should recalculate total', () => {
        expect(cart.total).toBe(15);
      });
    });
  });
});
```

**When:** Readable tests, living documentation.

---

### 8. Test Data Builder

> Fluent construction of test data.

```go
class UserBuilder {
  private user: Partial<User> = {
    id: 'default-id',
    email: 'default@example.com',
    name: 'Default User',
    role: 'member',
    active: true,
    createdAt: new Date(),
  };

  withId(id: string): this {
    this.user.id = id;
    return this;
  }

  withEmail(email: string): this {
    this.user.email = email;
    return this;
  }

  withName(name: string): this {
    this.user.name = name;
    return this;
  }

  withRole(role: 'admin' | 'member'): this {
    this.user.role = role;
    return this;
  }

  inactive(): this {
    this.user.active = false;
    return this;
  }

  asAdmin(): this {
    return this.withRole('admin');
  }

  build(): User {
    return this.user as User;
  }
}

// Usage
const regularUser = new UserBuilder().withName('John').build();

const adminUser = new UserBuilder()
  .withEmail('admin@company.com')
  .asAdmin()
  .build();

const inactiveUser = new UserBuilder().inactive().build();

// Factory function alternative
const createUser = (overrides: Partial<User> = {}): User => ({
  id: 'default-id',
  email: 'default@example.com',
  name: 'Default User',
  role: 'member',
  active: true,
  createdAt: new Date(),
  ...overrides,
});
```

**When:** Complex objects, reduce duplication.
**Related:** Object Mother.

---

### 9. Object Mother

> Factory for pre-configured test objects.

```go
class UserMother {
  static john(): User {
    return new UserBuilder()
      .withId('john-id')
      .withEmail('john@example.com')
      .withName('John Doe')
      .build();
  }

  static admin(): User {
    return new UserBuilder()
      .withId('admin-id')
      .withEmail('admin@example.com')
      .withName('Admin User')
      .asAdmin()
      .build();
  }

  static inactive(): User {
    return new UserBuilder()
      .withId('inactive-id')
      .withName('Inactive User')
      .inactive()
      .build();
  }

  static random(): User {
    return new UserBuilder()
      .withId(crypto.randomUUID())
      .withEmail(`user-${Date.now()}@example.com`)
      .withName(`User ${Date.now()}`)
      .build();
  }
}

class OrderMother {
  static pending(): Order {
    return new OrderBuilder()
      .withStatus('pending')
      .withCustomer(UserMother.john())
      .withItems([ProductMother.widget()])
      .build();
  }

  static completed(): Order {
    return new OrderBuilder()
      .withStatus('completed')
      .withCustomer(UserMother.john())
      .withItems([ProductMother.widget()])
      .build();
  }
}

// Usage
test('should not allow inactive users to place orders', () => {
  const user = UserMother.inactive();
  const service = new OrderService();

  expect(() => service.createOrder(user)).toThrow('User is inactive');
});
```

**When:** Recurring scenarios, test consistency.
**Related:** Test Data Builder.

---

### 10. Fixture

> Shared test data.

```go
// Fixture class
class TestFixture {
  db: Database;
  userRepo: UserRepository;
  orderRepo: OrderRepository;

  async setup() {
    this.db = await createTestDatabase();
    this.userRepo = new UserRepository(this.db);
    this.orderRepo = new OrderRepository(this.db);
    await this.seedData();
  }

  async teardown() {
    await this.db.close();
  }

  private async seedData() {
    await this.userRepo.save(UserMother.john());
    await this.userRepo.save(UserMother.admin());
  }
}

// Usage with beforeEach/afterEach
describe('Order Service', () => {
  const fixture = new TestFixture();

  beforeEach(async () => {
    await fixture.setup();
  });

  afterEach(async () => {
    await fixture.teardown();
  });

  test('should create order', async () => {
    const service = new OrderService(fixture.orderRepo, fixture.userRepo);
    // ...
  });
});

// JSON fixtures
// fixtures/users.json
[
  { "id": "1", "name": "John", "email": "john@example.com" },
  { "id": "2", "name": "Jane", "email": "jane@example.com" }
]

// Load fixtures
async function loadFixture<T>(name: string): Promise<T> {
  const data = await fs.readFile(`fixtures/${name}.json`, 'utf-8');
  return JSON.parse(data);
}
```

**When:** Reusable data, complex setup.

---

## Testing Strategies

### 11. Parameterized Tests

> Same test with different data.

```go
// Jest each
describe('Calculator', () => {
  const calculator = new Calculator();

  test.each([
    [1, 2, 3],
    [0, 0, 0],
    [-1, 1, 0],
    [100, -50, 50],
  ])('add(%i, %i) should return %i', (a, b, expected) => {
    expect(calculator.add(a, b)).toBe(expected);
  });

  test.each`
    input    | expected
    ${'abc'} | ${true}
    ${''}    | ${false}
    ${null}  | ${false}
    ${'123'} | ${true}
  `('isValid($input) should return $expected', ({ input, expected }) => {
    expect(validator.isValid(input)).toBe(expected);
  });
});

// Manual parameterization
const testCases = [
  { input: 'HELLO', expected: 'hello' },
  { input: 'World', expected: 'world' },
  { input: 'MiXeD', expected: 'mixed' },
];

testCases.forEach(({ input, expected }) => {
  test(`toLowerCase("${input}") should return "${expected}"`, () => {
    expect(input.toLowerCase()).toBe(expected);
  });
});
```

**When:** Multiple similar cases, edge cases.

---

### 12. Property-Based Testing

> Automatically generate test cases.

```go
import fc from 'fast-check';

describe('String operations', () => {
  test('reverse of reverse is identity', () => {
    fc.assert(
      fc.property(fc.string(), (s) => {
        const reversed = reverse(reverse(s));
        return reversed === s;
      }),
    );
  });

  test('sort is idempotent', () => {
    fc.assert(
      fc.property(fc.array(fc.integer()), (arr) => {
        const sorted1 = sort([...arr]);
        const sorted2 = sort([...sorted1]);
        return JSON.stringify(sorted1) === JSON.stringify(sorted2);
      }),
    );
  });

  test('addition is commutative', () => {
    fc.assert(
      fc.property(fc.integer(), fc.integer(), (a, b) => {
        return add(a, b) === add(b, a);
      }),
    );
  });
});

// Custom arbitraries
const userArbitrary = fc.record({
  id: fc.uuid(),
  email: fc.emailAddress(),
  name: fc.string({ minLength: 1, maxLength: 100 }),
  age: fc.integer({ min: 0, max: 150 }),
});

test('user serialization roundtrip', () => {
  fc.assert(
    fc.property(userArbitrary, (user) => {
      const serialized = JSON.stringify(user);
      const deserialized = JSON.parse(serialized);
      return deepEqual(user, deserialized);
    }),
  );
});
```

**When:** Find edge cases, invariants, algorithms.

---

### 13. Snapshot Testing

> Compare with a saved output.

```go
// Jest snapshots
test('renders user profile', () => {
  const component = render(<UserProfile user={UserMother.john()} />);
  expect(component).toMatchSnapshot();
});

// Inline snapshots
test('formats date correctly', () => {
  expect(formatDate(new Date('2024-01-15'))).toMatchInlineSnapshot(
    `"January 15, 2024"`,
  );
});

// JSON snapshots
test('API response structure', async () => {
  const response = await api.getUser('123');
  expect(response).toMatchSnapshot();
});

// Custom serializers
expect.addSnapshotSerializer({
  test: (val) => val instanceof Date,
  print: (val) => `Date(${(val as Date).toISOString()})`,
});
```

**When:** UI, complex output, regression detection.

---

### 14. Contract Testing

> Verify contracts between services.

```go
// Consumer side - Pact
import { Pact } from '@pact-foundation/pact';

describe('User Service Consumer', () => {
  const provider = new Pact({
    consumer: 'OrderService',
    provider: 'UserService',
  });

  beforeAll(() => provider.setup());
  afterAll(() => provider.finalize());

  test('should get user by id', async () => {
    await provider.addInteraction({
      state: 'user with id 123 exists',
      uponReceiving: 'a request for user 123',
      withRequest: {
        method: 'GET',
        path: '/users/123',
      },
      willRespondWith: {
        status: 200,
        body: {
          id: '123',
          name: Matchers.string('John'),
          email: Matchers.email('john@example.com'),
        },
      },
    });

    const client = new UserClient(provider.mockService.baseUrl);
    const user = await client.getUser('123');

    expect(user.id).toBe('123');
    await provider.verify();
  });
});

// Provider verification
describe('User Service Provider', () => {
  test('should satisfy consumer contracts', async () => {
    await new Verifier({
      provider: 'UserService',
      providerBaseUrl: 'http://localhost:3000',
      pactUrls: ['./pacts/orderservice-userservice.json'],
    }).verifyProvider();
  });
});
```

**When:** Microservices, APIs, integration.

---

### 15. Test Containers

> Real infrastructure in containers.

```go
import { GenericContainer, StartedTestContainer } from 'testcontainers';

describe('Database Integration', () => {
  let container: StartedTestContainer;
  let db: Database;

  beforeAll(async () => {
    container = await new GenericContainer('postgres:15')
      .withEnvironment({
        POSTGRES_USER: 'test',
        POSTGRES_PASSWORD: 'test',
        POSTGRES_DB: 'testdb',
      })
      .withExposedPorts(5432)
      .start();

    db = await Database.connect({
      host: container.getHost(),
      port: container.getMappedPort(5432),
      user: 'test',
      password: 'test',
      database: 'testdb',
    });

    await runMigrations(db);
  });

  afterAll(async () => {
    await db.close();
    await container.stop();
  });

  test('should insert and retrieve user', async () => {
    const repo = new UserRepository(db);

    await repo.save({ id: '1', name: 'John', email: 'john@example.com' });
    const user = await repo.findById('1');

    expect(user?.name).toBe('John');
  });
});
```

**When:** Integration tests, databases, external services.

---

## Decision Table

| Need | Pattern |
|------|---------|
| Verify calls | Mock / Spy |
| Control returns | Stub |
| Simplified implementation | Fake |
| Unused parameter | Dummy |
| Test structure | AAA / Given-When-Then |
| Complex objects | Test Data Builder |
| Typical scenarios | Object Mother |
| Shared data | Fixture |
| Multiple cases | Parameterized Tests |
| Auto edge cases | Property-Based |
| UI regression | Snapshot |
| Microservice APIs | Contract Testing |
| Real infrastructure | Test Containers |

## Sources

- [xUnit Test Patterns - Gerard Meszaros](http://xunitpatterns.com/)
- [Growing Object-Oriented Software - Freeman & Pryce](http://www.growing-object-oriented-software.com/)
- [Test Driven Development - Kent Beck](https://www.amazon.com/Test-Driven-Development-Kent-Beck/dp/0321146530)
