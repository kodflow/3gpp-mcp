# Enterprise Patterns (PoEAA)

Patterns from Martin Fowler - Patterns of Enterprise Application Architecture.

## Domain Logic Patterns

### 1. Transaction Script

> Procedure that handles a complete business transaction.

```go
class OrderService {
  async placeOrder(customerId: string, items: OrderItem[]) {
    // All logic in a single procedure
    const customer = await this.customerRepo.find(customerId);
    if (!customer) throw new Error('Customer not found');

    const order = new Order(customer);
    for (const item of items) {
      const product = await this.productRepo.find(item.productId);
      if (product.stock < item.quantity) {
        throw new Error('Insufficient stock');
      }
      order.addItem(product, item.quantity);
      product.stock -= item.quantity;
      await this.productRepo.save(product);
    }

    await this.orderRepo.save(order);
    await this.emailService.sendConfirmation(customer, order);
    return order;
  }
}
```

**When:** Simple logic, CRUD, small applications.
**Related to:** Service Layer.

---

### 2. Domain Model

> Business objects with behaviors and rules.

```go
class Order {
  private items: OrderItem[] = [];
  private status: OrderStatus = 'draft';

  addItem(product: Product, quantity: number) {
    if (this.status !== 'draft') {
      throw new Error('Cannot modify non-draft order');
    }
    const existing = this.items.find((i) => i.product.id === product.id);
    if (existing) {
      existing.quantity += quantity;
    } else {
      this.items.push(new OrderItem(product, quantity));
    }
  }

  submit() {
    if (this.items.length === 0) {
      throw new Error('Cannot submit empty order');
    }
    this.status = 'submitted';
  }

  get total(): Money {
    return this.items.reduce(
      (sum, item) => sum.add(item.subtotal),
      Money.zero(),
    );
  }
}
```

**When:** Complex business logic, rules, validations.
**Related to:** DDD, Rich Domain Model.

---

### 3. Table Module

> One class per table with methods.

```go
class ProductTable {
  constructor(private db: Database) {}

  async findById(id: string): Promise<ProductRow> {
    return this.db.query('SELECT * FROM products WHERE id = ?', [id]);
  }

  async findByCategory(category: string): Promise<ProductRow[]> {
    return this.db.query('SELECT * FROM products WHERE category = ?', [category]);
  }

  async updatePrice(id: string, price: number) {
    return this.db.execute('UPDATE products SET price = ? WHERE id = ?', [price, id]);
  }

  async calculateTotalValue(): Promise<number> {
    const result = await this.db.query('SELECT SUM(price * stock) FROM products');
    return result[0].sum;
  }
}
```

**When:** .NET DataTable style, moderate logic.
**Related to:** Table Data Gateway.

---

### 4. Service Layer

> Coordination layer for business operations.

```go
class OrderApplicationService {
  constructor(
    private orderRepo: OrderRepository,
    private inventoryService: InventoryService,
    private paymentService: PaymentService,
    private notificationService: NotificationService,
  ) {}

  @Transactional()
  async placeOrder(dto: PlaceOrderDTO): Promise<OrderDTO> {
    // Coordinates but does not contain business logic
    const order = Order.create(dto.customerId, dto.items);

    await this.inventoryService.reserve(order.items);

    try {
      await this.paymentService.charge(order.customerId, order.total);
    } catch (e) {
      await this.inventoryService.release(order.items);
      throw e;
    }

    await this.orderRepo.save(order);
    await this.notificationService.notifyOrderPlaced(order);

    return OrderDTO.from(order);
  }
}
```

**When:** Coordination, transactions, business facade.
**Related to:** Facade, Domain Model.

---

## Data Source Patterns

### 5. Table Data Gateway

> One class per table for CRUD.

```go
class ProductGateway {
  constructor(private db: Database) {}

  async find(id: string): Promise<ProductRow | null> {
    const rows = await this.db.query('SELECT * FROM products WHERE id = ?', [id]);
    return rows[0] || null;
  }

  async findAll(): Promise<ProductRow[]> {
    return this.db.query('SELECT * FROM products');
  }

  async insert(product: ProductRow): Promise<void> {
    await this.db.execute(
      'INSERT INTO products (id, name, price) VALUES (?, ?, ?)',
      [product.id, product.name, product.price],
    );
  }

  async update(product: ProductRow): Promise<void> {
    await this.db.execute(
      'UPDATE products SET name = ?, price = ? WHERE id = ?',
      [product.name, product.price, product.id],
    );
  }

  async delete(id: string): Promise<void> {
    await this.db.execute('DELETE FROM products WHERE id = ?', [id]);
  }
}
```

**When:** Simple data access, no ORM.
**Related to:** Row Data Gateway, Data Mapper.

---

### 6. Row Data Gateway

> One object per row with persistence.

```go
class ProductRow {
  constructor(
    private db: Database,
    public id: string,
    public name: string,
    public price: number,
  ) {}

  static async find(db: Database, id: string): Promise<ProductRow | null> {
    const rows = await db.query('SELECT * FROM products WHERE id = ?', [id]);
    if (!rows[0]) return null;
    return new ProductRow(db, rows[0].id, rows[0].name, rows[0].price);
  }

  async save(): Promise<void> {
    await this.db.execute(
      'UPDATE products SET name = ?, price = ? WHERE id = ?',
      [this.name, this.price, this.id],
    );
  }

  async delete(): Promise<void> {
    await this.db.execute('DELETE FROM products WHERE id = ?', [this.id]);
  }
}
```

**When:** Simple Active Record, without a full ORM.
**Related to:** Active Record.

---

### 7. Active Record

> Object that encapsulates a row + business logic + persistence.

```go
class User extends ActiveRecord {
  @Column() email: string;
  @Column() passwordHash: string;
  @Column() role: string;

  static async findByEmail(email: string): Promise<User | null> {
    return this.findOne({ email });
  }

  async setPassword(password: string) {
    this.passwordHash = await bcrypt.hash(password, 10);
  }

  async checkPassword(password: string): Promise<boolean> {
    return bcrypt.compare(password, this.passwordHash);
  }

  isAdmin(): boolean {
    return this.role === 'admin';
  }
}

// Usage
const user = new User();
user.email = 'john@example.com';
await user.setPassword('secret');
await user.save();
```

**When:** Simple CRUD with little logic, Rails/Django style.
**Related to:** Row Data Gateway, Domain Model.

---

### 8. Data Mapper

> Completely separates object and persistence.

```go
// Domain object - no dependency on DB
class Product {
  constructor(
    public readonly id: string,
    public name: string,
    public price: Money,
    private _stock: number,
  ) {}

  reduceStock(quantity: number) {
    if (quantity > this._stock) throw new Error('Insufficient stock');
    this._stock -= quantity;
  }

  get stock() { return this._stock; }
}

// Mapper - translates between domain and DB
class ProductMapper {
  constructor(private db: Database) {}

  async find(id: string): Promise<Product | null> {
    const row = await this.db.query('SELECT * FROM products WHERE id = ?', [id]);
    if (!row[0]) return null;
    return this.toDomain(row[0]);
  }

  async save(product: Product): Promise<void> {
    await this.db.execute(
      'UPDATE products SET name = ?, price = ?, stock = ? WHERE id = ?',
      [product.name, product.price.amount, product.stock, product.id],
    );
  }

  private toDomain(row: any): Product {
    return new Product(row.id, row.name, Money.of(row.price), row.stock);
  }
}
```

**When:** Rich domain model, separation of concerns, testability.
**Related to:** Repository, Domain Model.

---

## Object-Relational Behavioral

### 9. Unit of Work

> Maintains the list of modified objects for a transaction.

```go
class UnitOfWork {
  private newObjects = new Set<Entity>();
  private dirtyObjects = new Set<Entity>();
  private removedObjects = new Set<Entity>();

  registerNew(entity: Entity) {
    this.newObjects.add(entity);
  }

  registerDirty(entity: Entity) {
    if (!this.newObjects.has(entity)) {
      this.dirtyObjects.add(entity);
    }
  }

  registerRemoved(entity: Entity) {
    this.newObjects.delete(entity);
    this.dirtyObjects.delete(entity);
    this.removedObjects.add(entity);
  }

  async commit() {
    await this.insertNew();
    await this.updateDirty();
    await this.deleteRemoved();
    this.clear();
  }

  private async insertNew() {
    for (const entity of this.newObjects) {
      await this.mapper(entity).insert(entity);
    }
  }

  private async updateDirty() {
    for (const entity of this.dirtyObjects) {
      await this.mapper(entity).update(entity);
    }
  }

  private async deleteRemoved() {
    for (const entity of this.removedObjects) {
      await this.mapper(entity).delete(entity);
    }
  }
}
```

**When:** ORM, complex transactions, batch updates.
**Related to:** Repository, Identity Map.

---

### 10. Identity Map

> Cache of loaded objects by identity.

```go
class IdentityMap<T extends { id: string }> {
  private map = new Map<string, T>();

  get(id: string): T | undefined {
    return this.map.get(id);
  }

  add(entity: T) {
    this.map.set(entity.id, entity);
  }

  remove(id: string) {
    this.map.delete(id);
  }

  clear() {
    this.map.clear();
  }
}

class ProductRepository {
  private identityMap = new IdentityMap<Product>();

  async find(id: string): Promise<Product | null> {
    // Check identity map first
    const cached = this.identityMap.get(id);
    if (cached) return cached;

    // Load from DB
    const product = await this.mapper.find(id);
    if (product) {
      this.identityMap.add(product);
    }
    return product;
  }
}
```

**When:** Avoid in-memory duplicates, consistency.
**Related to:** Unit of Work, Cache.

---

### 11. Lazy Load

> Load data on demand.

```go
// Virtual Proxy
class LazyProduct {
  private _details: ProductDetails | null = null;

  constructor(
    public readonly id: string,
    private loader: () => Promise<ProductDetails>,
  ) {}

  async getDetails(): Promise<ProductDetails> {
    if (!this._details) {
      this._details = await this.loader();
    }
    return this._details;
  }
}

// Ghost
class Product {
  private loaded = false;
  private _name?: string;
  private _price?: Money;

  constructor(
    public readonly id: string,
    private loader: (id: string) => Promise<ProductData>,
  ) {}

  private async ensureLoaded() {
    if (!this.loaded) {
      const data = await this.loader(this.id);
      this._name = data.name;
      this._price = data.price;
      this.loaded = true;
    }
  }

  async getName(): Promise<string> {
    await this.ensureLoaded();
    return this._name!;
  }
}
```

**Variants:** Virtual Proxy, Value Holder, Ghost.
**When:** Expensive relations, partial loading.
**Related to:** Proxy, Virtual Proxy.

---

## Object-Relational Structural

### 12. Foreign Key Mapping

> Map relations via foreign keys.

```go
class OrderMapper {
  async find(id: string): Promise<Order> {
    const row = await this.db.query('SELECT * FROM orders WHERE id = ?', [id]);
    const order = new Order(row.id, row.date);

    // Lazy load customer via foreign key
    order.customerId = row.customer_id;
    order.getCustomer = async () => {
      return this.customerMapper.find(row.customer_id);
    };

    return order;
  }

  async findWithCustomer(id: string): Promise<Order> {
    const row = await this.db.query(`
      SELECT o.*, c.name as customer_name, c.email as customer_email
      FROM orders o
      JOIN customers c ON o.customer_id = c.id
      WHERE o.id = ?
    `, [id]);

    const customer = new Customer(row.customer_id, row.customer_name, row.customer_email);
    const order = new Order(row.id, row.date, customer);
    return order;
  }
}
```

**When:** 1-N, N-1 relations.
**Related to:** Association Table Mapping.

---

### 13. Association Table Mapping

> Junction table for N-N relations.

```go
class ProductCategoryMapper {
  async findCategoriesForProduct(productId: string): Promise<Category[]> {
    const rows = await this.db.query(`
      SELECT c.* FROM categories c
      JOIN product_categories pc ON c.id = pc.category_id
      WHERE pc.product_id = ?
    `, [productId]);
    return rows.map((r) => new Category(r.id, r.name));
  }

  async addCategoryToProduct(productId: string, categoryId: string) {
    await this.db.execute(
      'INSERT INTO product_categories (product_id, category_id) VALUES (?, ?)',
      [productId, categoryId],
    );
  }

  async removeCategoryFromProduct(productId: string, categoryId: string) {
    await this.db.execute(
      'DELETE FROM product_categories WHERE product_id = ? AND category_id = ?',
      [productId, categoryId],
    );
  }
}
```

**When:** Many-to-many relations.
**Related to:** Foreign Key Mapping.

---

### 14. Embedded Value

> Map a value object into the columns of the parent table.

```go
// Value Object
class Address {
  constructor(
    public street: string,
    public city: string,
    public zipCode: string,
    public country: string,
  ) {}
}

// Entity with embedded value
class Customer {
  constructor(
    public id: string,
    public name: string,
    public address: Address,
  ) {}
}

// Mapper
class CustomerMapper {
  async find(id: string): Promise<Customer> {
    const row = await this.db.query('SELECT * FROM customers WHERE id = ?', [id]);
    return new Customer(
      row.id,
      row.name,
      new Address(row.street, row.city, row.zip_code, row.country),
    );
  }

  async save(customer: Customer) {
    await this.db.execute(`
      UPDATE customers SET
        name = ?, street = ?, city = ?, zip_code = ?, country = ?
      WHERE id = ?
    `, [
      customer.name,
      customer.address.street,
      customer.address.city,
      customer.address.zipCode,
      customer.address.country,
      customer.id,
    ]);
  }
}
```

**When:** Value objects without a dedicated table.
**Related to:** Value Object, Serialized LOB.

---

### 15. Serialized LOB

> Serialize an object graph into a field.

```go
class ProductMapper {
  async find(id: string): Promise<Product> {
    const row = await this.db.query('SELECT * FROM products WHERE id = ?', [id]);
    return new Product(
      row.id,
      row.name,
      JSON.parse(row.attributes), // Serialized LOB
      JSON.parse(row.metadata),
    );
  }

  async save(product: Product) {
    await this.db.execute(`
      UPDATE products SET
        name = ?, attributes = ?, metadata = ?
      WHERE id = ?
    `, [
      product.name,
      JSON.stringify(product.attributes),
      JSON.stringify(product.metadata),
      product.id,
    ]);
  }
}
```

**When:** Semi-structured data, flexible schema.
**Related to:** Embedded Value.

---

### 16. Inheritance Mapping

> Three strategies for mapping inheritance.

```go
// Single Table Inheritance
// A single table with discriminator
// employees(id, name, type, salary, hourly_rate)
class EmployeeMapper {
  async find(id: string): Promise<Employee> {
    const row = await this.db.query('SELECT * FROM employees WHERE id = ?', [id]);
    switch (row.type) {
      case 'salaried':
        return new SalariedEmployee(row.id, row.name, row.salary);
      case 'hourly':
        return new HourlyEmployee(row.id, row.name, row.hourly_rate);
      default:
        throw new Error('Unknown type');
    }
  }
}

// Class Table Inheritance
// employees(id, name) + salaried_employees(id, salary) + hourly_employees(id, hourly_rate)
class EmployeeMapper {
  async find(id: string): Promise<Employee> {
    const base = await this.db.query('SELECT * FROM employees WHERE id = ?', [id]);
    const salaried = await this.db.query('SELECT * FROM salaried_employees WHERE id = ?', [id]);
    if (salaried[0]) {
      return new SalariedEmployee(base.id, base.name, salaried[0].salary);
    }
    const hourly = await this.db.query('SELECT * FROM hourly_employees WHERE id = ?', [id]);
    return new HourlyEmployee(base.id, base.name, hourly[0].hourly_rate);
  }
}

// Concrete Table Inheritance
// salaried_employees(id, name, salary) + hourly_employees(id, name, hourly_rate)
```

**Strategies:**

- **Single Table**: One table, discriminator column
- **Class Table**: One table per class in the hierarchy
- **Concrete Table**: One table per concrete class

**When:** Persisted object hierarchies.
**Related to:** Polymorphism.

---

## Web Presentation

### 17. MVC (Model-View-Controller)

> Separate data, presentation, and control.

```go
// Model
class UserModel {
  constructor(
    public id: string,
    public name: string,
    public email: string,
  ) {}
}

// View
class UserView {
  render(user: UserModel): string {
    return `<div>
      <h1>${user.name}</h1>
      <p>${user.email}</p>
    </div>`;
  }
}

// Controller
class UserController {
  constructor(
    private userService: UserService,
    private view: UserView,
  ) {}

  async show(req: Request, res: Response) {
    const user = await this.userService.find(req.params.id);
    const html = this.view.render(user);
    res.send(html);
  }
}
```

**When:** Web applications, separation of concerns.
**Related to:** MVP, MVVM.

---

### 18. Page Controller

> One controller per page/action.

```go
// /users/show.ts
class ShowUserController {
  async handle(req: Request, res: Response) {
    const user = await this.userService.find(req.params.id);
    return res.render('users/show', { user });
  }
}

// /users/edit.ts
class EditUserController {
  async handle(req: Request, res: Response) {
    if (req.method === 'GET') {
      const user = await this.userService.find(req.params.id);
      return res.render('users/edit', { user });
    }
    if (req.method === 'POST') {
      await this.userService.update(req.params.id, req.body);
      return res.redirect(`/users/${req.params.id}`);
    }
  }
}
```

**When:** Simple applications, distinct pages.
**Related to:** Front Controller.

---

### 19. Front Controller

> Single entry point for all requests.

```go
class FrontController {
  private routes = new Map<string, Controller>();

  register(pattern: string, controller: Controller) {
    this.routes.set(pattern, controller);
  }

  async dispatch(req: Request, res: Response) {
    // Pre-processing
    await this.authenticate(req);
    await this.authorize(req);

    // Find and execute controller
    const controller = this.findController(req.path);
    await controller.handle(req, res);

    // Post-processing
    await this.log(req, res);
  }

  private findController(path: string): Controller {
    for (const [pattern, controller] of this.routes) {
      if (this.matches(path, pattern)) {
        return controller;
      }
    }
    throw new NotFoundError();
  }
}
```

**When:** Web frameworks, middleware, interceptors.
**Related to:** Page Controller, Intercepting Filter.

---

### 20. Template View

> HTML with placeholders.

```go
// template.html
// <h1>{{title}}</h1>
// <ul>
//   {{#each items}}
//     <li>{{name}}</li>
//   {{/each}}
// </ul>

class TemplateView {
  constructor(private engine: TemplateEngine) {}

  render(template: string, data: object): string {
    return this.engine.render(template, data);
  }
}

// Usage
const html = view.render('product/list', {
  title: 'Products',
  items: products,
});
```

**When:** Dynamic HTML, server-side rendering.
**Related to:** Transform View.

---

### 21. Transform View

> Transform data into output (XSLT, JSON, etc.).

```go
class JsonTransformView {
  transform(data: any): string {
    return JSON.stringify(data, null, 2);
  }
}

class XmlTransformView {
  transform(data: any): string {
    return this.objectToXml(data);
  }

  private objectToXml(obj: any, root = 'root'): string {
    let xml = `<${root}>`;
    for (const [key, value] of Object.entries(obj)) {
      if (Array.isArray(value)) {
        value.forEach((item) => {
          xml += this.objectToXml(item, key);
        });
      } else if (typeof value === 'object') {
        xml += this.objectToXml(value, key);
      } else {
        xml += `<${key}>${value}</${key}>`;
      }
    }
    xml += `</${root}>`;
    return xml;
  }
}
```

**When:** APIs, multiple formats, XSLT.
**Related to:** Template View, Content Negotiation.

---

## Distribution Patterns

### 22. Remote Facade

> Simplified interface for remote calls.

```go
// Fine-grained domain objects
class Order { /* many methods */ }
class OrderItem { /* many methods */ }
class Customer { /* many methods */ }

// Coarse-grained remote facade
class OrderFacade {
  @RemoteMethod()
  async placeOrder(dto: PlaceOrderDTO): Promise<OrderConfirmation> {
    // Single remote call does many operations
    const customer = await this.customerRepo.find(dto.customerId);
    const order = new Order(customer);

    for (const item of dto.items) {
      order.addItem(item.productId, item.quantity);
    }

    await this.orderRepo.save(order);
    return { orderId: order.id, total: order.total };
  }
}
```

**When:** APIs, microservices, reduce round-trips.
**Related to:** Facade, DTO.

---

### 23. Data Transfer Object (DTO)

> Object for transferring data between layers.

```go
// DTOs - no behavior, just data
class OrderDTO {
  id: string;
  customerName: string;
  items: OrderItemDTO[];
  total: number;

  static from(order: Order): OrderDTO {
    return {
      id: order.id,
      customerName: order.customer.name,
      items: order.items.map(OrderItemDTO.from),
      total: order.total.amount,
    };
  }
}

class OrderItemDTO {
  productName: string;
  quantity: number;
  unitPrice: number;

  static from(item: OrderItem): OrderItemDTO {
    return {
      productName: item.product.name,
      quantity: item.quantity,
      unitPrice: item.product.price.amount,
    };
  }
}
```

**When:** APIs, serialization, layer isolation.
**Related to:** Remote Facade, Assembler.

---

## Offline Concurrency

### 24. Optimistic Offline Lock

> Detect conflicts at save time.

```go
class ProductMapper {
  async update(product: Product): Promise<void> {
    const result = await this.db.execute(`
      UPDATE products
      SET name = ?, price = ?, version = version + 1
      WHERE id = ? AND version = ?
    `, [product.name, product.price, product.id, product.version]);

    if (result.affectedRows === 0) {
      throw new OptimisticLockException('Product was modified by another user');
    }

    product.version++;
  }
}

// Usage
try {
  await productMapper.update(product);
} catch (e) {
  if (e instanceof OptimisticLockException) {
    // Reload and retry or notify user
    const fresh = await productMapper.find(product.id);
    // Merge changes...
  }
}
```

**When:** Rare conflicts, no long locks.
**Related to:** Pessimistic Lock.

---

### 25. Pessimistic Offline Lock

> Lock the resource before modification.

```go
class LockManager {
  private locks = new Map<string, { userId: string; expires: Date }>();

  acquire(resourceId: string, userId: string, ttlMinutes = 30): boolean {
    const existing = this.locks.get(resourceId);
    if (existing && existing.expires > new Date() && existing.userId !== userId) {
      return false; // Already locked by another user
    }

    this.locks.set(resourceId, {
      userId,
      expires: new Date(Date.now() + ttlMinutes * 60000),
    });
    return true;
  }

  release(resourceId: string, userId: string): boolean {
    const lock = this.locks.get(resourceId);
    if (lock && lock.userId === userId) {
      this.locks.delete(resourceId);
      return true;
    }
    return false;
  }

  isLocked(resourceId: string): boolean {
    const lock = this.locks.get(resourceId);
    return lock !== undefined && lock.expires > new Date();
  }
}
```

**When:** Frequent conflicts, long editing sessions.
**Related to:** Optimistic Lock.

---

### 26. Coarse-Grained Lock

> Lock an entire aggregate.

```go
class OrderLock {
  constructor(private lockManager: LockManager) {}

  async acquireForOrder(orderId: string, userId: string) {
    // Lock entire order aggregate
    const order = await this.orderRepo.find(orderId);

    const locked = await this.lockManager.acquire(`order:${orderId}`, userId);
    if (!locked) {
      throw new LockedException('Order is being edited by another user');
    }

    // Also lock all items
    for (const item of order.items) {
      await this.lockManager.acquire(`orderitem:${item.id}`, userId);
    }

    return order;
  }
}
```

**When:** DDD aggregates, strong consistency.
**Related to:** Aggregate, Pessimistic Lock.

---

## Session State

### 27. Client Session State

> State stored on the client side.

```go
// JWT Token
class ClientSessionState {
  createToken(user: User): string {
    return jwt.sign({
      userId: user.id,
      role: user.role,
      preferences: user.preferences,
    }, SECRET, { expiresIn: '1h' });
  }

  parseToken(token: string): SessionData {
    return jwt.verify(token, SECRET) as SessionData;
  }
}

// Cookie
class CookieSession {
  save(res: Response, data: SessionData) {
    res.cookie('session', JSON.stringify(data), {
      httpOnly: true,
      secure: true,
      sameSite: 'strict',
    });
  }

  load(req: Request): SessionData | null {
    const cookie = req.cookies.session;
    return cookie ? JSON.parse(cookie) : null;
  }
}
```

**When:** Stateless servers, scalability.
**Related to:** Server Session State.

---

### 28. Server Session State

> State stored on the server side.

```go
class ServerSessionState {
  private sessions = new Map<string, SessionData>();

  create(data: SessionData): string {
    const sessionId = crypto.randomUUID();
    this.sessions.set(sessionId, data);
    return sessionId;
  }

  get(sessionId: string): SessionData | undefined {
    return this.sessions.get(sessionId);
  }

  update(sessionId: string, data: Partial<SessionData>) {
    const session = this.sessions.get(sessionId);
    if (session) {
      Object.assign(session, data);
    }
  }

  destroy(sessionId: string) {
    this.sessions.delete(sessionId);
  }
}

// Redis-backed for distributed systems
class RedisSessionState {
  async get(sessionId: string): Promise<SessionData | null> {
    const data = await this.redis.get(`session:${sessionId}`);
    return data ? JSON.parse(data) : null;
  }

  async save(sessionId: string, data: SessionData, ttlSeconds = 3600) {
    await this.redis.setex(`session:${sessionId}`, ttlSeconds, JSON.stringify(data));
  }
}
```

**When:** Sensitive data, server control.
**Related to:** Client Session State.

---

### 29. Database Session State

> State stored in the database.

```go
class DatabaseSessionState {
  async create(data: SessionData): Promise<string> {
    const sessionId = crypto.randomUUID();
    await this.db.execute(`
      INSERT INTO sessions (id, data, expires_at)
      VALUES (?, ?, ?)
    `, [sessionId, JSON.stringify(data), this.expiresAt()]);
    return sessionId;
  }

  async get(sessionId: string): Promise<SessionData | null> {
    const rows = await this.db.query(`
      SELECT data FROM sessions
      WHERE id = ? AND expires_at > NOW()
    `, [sessionId]);
    return rows[0] ? JSON.parse(rows[0].data) : null;
  }

  async cleanup() {
    await this.db.execute('DELETE FROM sessions WHERE expires_at < NOW()');
  }
}
```

**When:** Persistence, survives restarts.
**Related to:** Server Session State.

---

## Decision Table

| Need | Pattern |
|------|---------|
| Simple logic/CRUD | Transaction Script |
| Rich business logic | Domain Model |
| Service coordination | Service Layer |
| Simple per-table CRUD | Table/Row Data Gateway |
| Self-persisting objects | Active Record |
| Domain/persistence separation | Data Mapper |
| Change tracking | Unit of Work |
| Avoid in-memory duplicates | Identity Map |
| Deferred loading | Lazy Load |
| N-N relations | Association Table Mapping |
| Value objects | Embedded Value |
| Flexible data | Serialized LOB |
| Inheritance in DB | Inheritance Mapping |
| Reduce round-trips | Remote Facade + DTO |
| Rare conflicts | Optimistic Lock |
| Frequent conflicts | Pessimistic Lock |

## Sources

- [Patterns of Enterprise Application Architecture - Martin Fowler](https://martinfowler.com/eaaCatalog/)
