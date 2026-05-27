# Messaging Patterns (EIP)

Enterprise Integration Patterns - Gregor Hohpe & Bobby Woolf.

## Message Construction

### 1. Command Message

> Message that requests an action.

```go
interface CommandMessage<T = unknown> {
  type: 'command';
  command: string;
  payload: T;
  correlationId: string;
  replyTo?: string;
}

// Usage
const command: CommandMessage<CreateOrderPayload> = {
  type: 'command',
  command: 'CreateOrder',
  payload: { customerId: '123', items: [...] },
  correlationId: crypto.randomUUID(),
  replyTo: 'order-responses',
};

await messageBus.send('orders', command);
```

**When:** Trigger actions, CQRS commands.
**Related to:** Document Message, Event Message.

---

### 2. Event Message

> Notification of a past fact.

```go
interface EventMessage<T = unknown> {
  type: 'event';
  event: string;
  payload: T;
  timestamp: Date;
  aggregateId: string;
  version: number;
}

const event: EventMessage<OrderCreatedPayload> = {
  type: 'event',
  event: 'OrderCreated',
  payload: { orderId: '456', total: 99.99 },
  timestamp: new Date(),
  aggregateId: '456',
  version: 1,
};

await eventBus.publish('order-events', event);
```

**When:** Event sourcing, notifications, decoupling.
**Related to:** Command Message, Observer.

---

### 3. Document Message

> Message containing data.

```go
interface DocumentMessage<T = unknown> {
  type: 'document';
  documentType: string;
  content: T;
  metadata: {
    source: string;
    timestamp: Date;
    version: string;
  };
}

const doc: DocumentMessage<CustomerData> = {
  type: 'document',
  documentType: 'CustomerProfile',
  content: {
    id: '123',
    name: 'John Doe',
    email: 'john@example.com',
  },
  metadata: {
    source: 'crm-system',
    timestamp: new Date(),
    version: '1.0',
  },
};
```

**When:** Data transfer, synchronization.
**Related to:** Command Message.

---

### 4. Request-Reply

> Message with expected response.

```go
class RequestReplyClient {
  private pending = new Map<string, Deferred<any>>();

  constructor(private channel: MessageChannel) {
    this.channel.subscribe('replies', (msg) => {
      const deferred = this.pending.get(msg.correlationId);
      if (deferred) {
        deferred.resolve(msg.payload);
        this.pending.delete(msg.correlationId);
      }
    });
  }

  async request<T, R>(destination: string, payload: T): Promise<R> {
    const correlationId = crypto.randomUUID();
    const deferred = new Deferred<R>();
    this.pending.set(correlationId, deferred);

    await this.channel.send(destination, {
      payload,
      correlationId,
      replyTo: 'replies',
    });

    return deferred.promise;
  }
}

// Usage
const result = await client.request('calculator', { operation: 'add', a: 1, b: 2 });
```

**When:** RPC over messaging, queries.
**Related to:** Correlation Identifier.

---

### 5. Correlation Identifier

> Link request and response.

```go
interface CorrelatedMessage {
  correlationId: string;
  causationId?: string; // ID of the message that caused this one
}

class MessageTracker {
  private correlations = new Map<string, Message[]>();

  track(message: CorrelatedMessage) {
    if (!this.correlations.has(message.correlationId)) {
      this.correlations.set(message.correlationId, []);
    }
    this.correlations.get(message.correlationId)!.push(message);
  }

  getConversation(correlationId: string): Message[] {
    return this.correlations.get(correlationId) || [];
  }
}
```

**When:** Transaction tracking, distributed debugging.
**Related to:** Request-Reply.

---

### 6. Message Sequence

> Set of ordered messages.

```go
interface SequencedMessage {
  sequenceId: string;
  sequenceNumber: number;
  sequenceSize: number;
  isLast: boolean;
  payload: Buffer;
}

class SequenceAssembler {
  private buffers = new Map<string, Map<number, Buffer>>();

  add(msg: SequencedMessage): Buffer | null {
    if (!this.buffers.has(msg.sequenceId)) {
      this.buffers.set(msg.sequenceId, new Map());
    }

    this.buffers.get(msg.sequenceId)!.set(msg.sequenceNumber, msg.payload);

    if (msg.isLast) {
      const parts = this.buffers.get(msg.sequenceId)!;
      if (parts.size === msg.sequenceSize) {
        const sorted = [...parts.entries()].sort((a, b) => a[0] - b[0]);
        this.buffers.delete(msg.sequenceId);
        return Buffer.concat(sorted.map((p) => p[1]));
      }
    }

    return null;
  }
}
```

**When:** Large messages, streaming.
**Related to:** Splitter, Aggregator.

---

### 7. Message Expiration

> TTL on messages.

```go
interface ExpirableMessage {
  expiresAt: Date;
  payload: unknown;
}

class ExpirationFilter {
  filter(message: ExpirableMessage): boolean {
    return new Date() < message.expiresAt;
  }
}

// Dead Letter Queue for expired messages
class MessageProcessor {
  constructor(
    private handler: (msg: any) => Promise<void>,
    private deadLetterQueue: MessageQueue,
  ) {}

  async process(msg: ExpirableMessage) {
    if (new Date() >= msg.expiresAt) {
      await this.deadLetterQueue.send({
        original: msg,
        reason: 'expired',
        expiredAt: new Date(),
      });
      return;
    }
    await this.handler(msg);
  }
}
```

**When:** Timeouts, perishable data.
**Related to:** Dead Letter Channel.

---

## Message Routing

### 8. Content-Based Router

> Route according to message content.

```go
class ContentBasedRouter {
  private routes = new Map<string, string>();

  addRoute(predicate: (msg: any) => boolean, destination: string) {
    // Implementation with predicates
  }

  route(message: any): string {
    // Route by message type
    switch (message.type) {
      case 'order':
        return message.priority === 'high' ? 'express-queue' : 'standard-queue';
      case 'return':
        return 'returns-queue';
      default:
        return 'default-queue';
    }
  }
}

// Usage
const router = new ContentBasedRouter();
const destination = router.route(message);
await messageBus.send(destination, message);
```

**When:** Dynamic routing, business rules.
**Related to:** Message Filter, Recipient List.

---

### 9. Message Filter

> Remove unwanted messages.

```go
type Predicate<T> = (message: T) => boolean;

class MessageFilter<T> {
  constructor(private predicate: Predicate<T>) {}

  filter(messages: T[]): T[] {
    return messages.filter(this.predicate);
  }

  async* filterStream(stream: AsyncIterable<T>): AsyncGenerator<T> {
    for await (const message of stream) {
      if (this.predicate(message)) {
        yield message;
      }
    }
  }
}

// Usage
const validOrderFilter = new MessageFilter<Order>(
  (order) => order.items.length > 0 && order.total > 0,
);
```

**When:** Validation, cleanup, security.
**Related to:** Content-Based Router.

---

### 10. Recipient List

> Send to multiple recipients.

```go
class RecipientList {
  constructor(private destinations: string[]) {}

  async send(message: any, channel: MessageChannel) {
    await Promise.all(
      this.destinations.map((dest) => channel.send(dest, message)),
    );
  }

  // Dynamic recipient list based on message
  static fromMessage(message: any): RecipientList {
    const destinations: string[] = [];

    if (message.requiresInventory) {
      destinations.push('inventory-service');
    }
    if (message.requiresPayment) {
      destinations.push('payment-service');
    }
    if (message.requiresShipping) {
      destinations.push('shipping-service');
    }

    return new RecipientList(destinations);
  }
}
```

**When:** Multicast, multiple notifications.
**Related to:** Publish-Subscribe.

---

### 11. Splitter

> Split a message into multiple.

```go
class OrderSplitter {
  split(order: Order): OrderItemMessage[] {
    return order.items.map((item, index) => ({
      originalOrderId: order.id,
      sequenceNumber: index,
      totalItems: order.items.length,
      item,
      customer: order.customer,
    }));
  }
}

// Generic splitter
class Splitter<T, U> {
  constructor(private splitFn: (message: T) => U[]) {}

  split(message: T): U[] {
    return this.splitFn(message);
  }
}
```

**When:** Parallel processing, distribution.
**Related to:** Aggregator.

---

### 12. Aggregator

> Combine multiple messages into one.

```go
class Aggregator<T, R> {
  private buffers = new Map<string, { items: T[]; expectedCount: number }>();

  constructor(
    private correlationFn: (msg: T) => string,
    private completionFn: (msgs: T[]) => boolean,
    private aggregateFn: (msgs: T[]) => R,
  ) {}

  add(message: T): R | null {
    const correlationId = this.correlationFn(message);

    if (!this.buffers.has(correlationId)) {
      this.buffers.set(correlationId, { items: [], expectedCount: 0 });
    }

    const buffer = this.buffers.get(correlationId)!;
    buffer.items.push(message);

    if (this.completionFn(buffer.items)) {
      this.buffers.delete(correlationId);
      return this.aggregateFn(buffer.items);
    }

    return null;
  }
}

// Usage
const orderAggregator = new Aggregator<OrderItemResult, OrderResult>(
  (msg) => msg.originalOrderId,
  (msgs) => msgs.length === msgs[0].totalItems,
  (msgs) => ({
    orderId: msgs[0].originalOrderId,
    results: msgs.map((m) => m.result),
  }),
);
```

**When:** After Splitter, waiting for multiple responses.
**Related to:** Splitter, Scatter-Gather.

---

### 13. Scatter-Gather

> Send and collect responses.

```go
class ScatterGather<T, R> {
  constructor(
    private destinations: string[],
    private timeout: number,
  ) {}

  async scatter(message: T): Promise<R[]> {
    const correlationId = crypto.randomUUID();
    const responses: R[] = [];
    const responsePromises: Promise<R>[] = [];

    for (const dest of this.destinations) {
      responsePromises.push(
        this.sendAndWait(dest, message, correlationId),
      );
    }

    const results = await Promise.allSettled(responsePromises);

    return results
      .filter((r): r is PromiseFulfilledResult<R> => r.status === 'fulfilled')
      .map((r) => r.value);
  }

  private async sendAndWait(dest: string, msg: T, correlationId: string): Promise<R> {
    // Implementation with timeout
    return Promise.race([
      this.channel.request(dest, msg, correlationId),
      this.timeoutPromise(),
    ]);
  }
}

// Usage - Price comparison
const priceChecker = new ScatterGather<Product, PriceQuote>(
  ['supplier-a', 'supplier-b', 'supplier-c'],
  5000,
);
const quotes = await priceChecker.scatter(product);
const bestPrice = quotes.reduce((min, q) => q.price < min.price ? q: min);
```

**When:** Comparison, best-of, quorum.
**Related to:** Recipient List, Aggregator.

---

### 14. Routing Slip

> Dynamic itinerary for the message.

```go
interface RoutingSlip {
  steps: string[];
  currentStep: number;
  history: { step: string; timestamp: Date; result: any }[];
}

interface RoutedMessage {
  payload: any;
  routingSlip: RoutingSlip;
}

class RoutingSlipProcessor {
  async process(message: RoutedMessage) {
    const { routingSlip } = message;

    if (routingSlip.currentStep >= routingSlip.steps.length) {
      return message; // Complete
    }

    const currentStep = routingSlip.steps[routingSlip.currentStep];
    const result = await this.executeStep(currentStep, message.payload);

    routingSlip.history.push({
      step: currentStep,
      timestamp: new Date(),
      result,
    });
    routingSlip.currentStep++;

    // Forward to next processor or return
    if (routingSlip.currentStep < routingSlip.steps.length) {
      const nextStep = routingSlip.steps[routingSlip.currentStep];
      await this.channel.send(nextStep, message);
    }

    return message;
  }
}
```

**When:** Dynamic workflows, pipelines.
**Related to:** Process Manager.

---

### 15. Process Manager

> Orchestrate a complex workflow.

```go
interface ProcessState {
  processId: string;
  currentStep: string;
  data: Record<string, any>;
  startedAt: Date;
  completedSteps: string[];
}

class OrderProcessManager {
  private processes = new Map<string, ProcessState>();

  async handleMessage(message: any) {
    const state = this.processes.get(message.processId) || this.createProcess(message);

    switch (state.currentStep) {
      case 'created':
        await this.validateOrder(state, message);
        break;
      case 'validated':
        await this.reserveInventory(state, message);
        break;
      case 'inventory_reserved':
        await this.processPayment(state, message);
        break;
      case 'payment_processed':
        await this.shipOrder(state, message);
        break;
      case 'shipped':
        this.completeProcess(state);
        break;
    }
  }

  private async validateOrder(state: ProcessState, message: any) {
    await this.send('validation-service', { orderId: state.processId, ...message });
    state.currentStep = 'validating';
  }

  // ... other steps
}
```

**When:** Sagas, orchestration, long-running processes.
**Related to:** Saga, Routing Slip.

---

## Message Transformation

### 16. Message Translator

> Convert between formats.

```go
interface MessageTranslator<S, T> {
  translate(source: S): T;
}

class XmlToJsonTranslator implements MessageTranslator<string, object> {
  translate(xml: string): object {
    // Parse XML to JSON
    return parseXml(xml);
  }
}

class LegacyOrderTranslator implements MessageTranslator<LegacyOrder, ModernOrder> {
  translate(legacy: LegacyOrder): ModernOrder {
    return {
      id: legacy.ORDER_ID,
      customer: {
        id: legacy.CUST_NO,
        name: `${legacy.FIRST_NM} ${legacy.LAST_NM}`,
      },
      items: legacy.ITEMS.map((i) => ({
        productId: i.PROD_ID,
        quantity: i.QTY,
        price: i.UNIT_PRC / 100, // Convert cents to dollars
      })),
      total: legacy.TOT_AMT / 100,
    };
  }
}
```

**When:** Legacy integration, multiple formats.
**Related to:** Adapter, Canonical Data Model.

---

### 17. Envelope Wrapper

> Add metadata to the message.

```go
interface Envelope<T> {
  header: {
    messageId: string;
    timestamp: Date;
    source: string;
    version: string;
    contentType: string;
  };
  body: T;
}

class EnvelopeWrapper {
  wrap<T>(message: T, source: string): Envelope<T> {
    return {
      header: {
        messageId: crypto.randomUUID(),
        timestamp: new Date(),
        source,
        version: '1.0',
        contentType: 'application/json',
      },
      body: message,
    };
  }

  unwrap<T>(envelope: Envelope<T>): T {
    return envelope.body;
  }
}
```

**When:** Metadata, transport agnostic.
**Related to:** Message, Header.

---

### 18. Content Enricher

> Add missing data.

```go
class OrderEnricher {
  constructor(
    private customerService: CustomerService,
    private productService: ProductService,
  ) {}

  async enrich(order: PartialOrder): Promise<EnrichedOrder> {
    const customer = await this.customerService.find(order.customerId);
    const products = await Promise.all(
      order.items.map((i) => this.productService.find(i.productId)),
    );

    return {
      ...order,
      customer: {
        name: customer.name,
        email: customer.email,
        address: customer.shippingAddress,
      },
      items: order.items.map((item, i) => ({
        ...item,
        productName: products[i].name,
        unitPrice: products[i].price,
      })),
      enrichedAt: new Date(),
    };
  }
}
```

**When:** Partial data, aggregation.
**Related to:** Content Filter.

---

### 19. Content Filter

> Remove unnecessary data.

```go
class SensitiveDataFilter {
  filter(order: FullOrder): PublicOrder {
    return {
      id: order.id,
      status: order.status,
      items: order.items.map((i) => ({
        name: i.productName,
        quantity: i.quantity,
      })),
      // Exclude: customer.creditCard, customer.ssn, internalNotes
    };
  }
}

// Generic filter
class ContentFilter<T, R> {
  constructor(private projection: (input: T) => R) {}

  filter(message: T): R {
    return this.projection(message);
  }
}
```

**When:** Security, privacy, reduce size.
**Related to:** Content Enricher.

---

### 20. Normalizer

> Transform various formats into a canonical format.

```go
interface CanonicalOrder {
  id: string;
  customer: { id: string; name: string };
  items: { sku: string; qty: number; price: number }[];
}

class OrderNormalizer {
  normalize(order: unknown, source: string): CanonicalOrder {
    switch (source) {
      case 'legacy':
        return this.fromLegacy(order as LegacyOrder);
      case 'partner-a':
        return this.fromPartnerA(order as PartnerAOrder);
      case 'web':
        return this.fromWeb(order as WebOrder);
      default:
        throw new Error(`Unknown source: ${source}`);
    }
  }

  private fromLegacy(order: LegacyOrder): CanonicalOrder {
    return {
      id: order.ORDER_NO,
      customer: { id: order.CUST_ID, name: order.CUST_NAME },
      items: order.LINES.map((l) => ({
        sku: l.ITEM_NO,
        qty: l.QUANTITY,
        price: l.AMOUNT,
      })),
    };
  }

  // ... other transformers
}
```

**When:** Multiple sources, integration.
**Related to:** Canonical Data Model, Translator.

---

## Message Endpoints

### 21. Polling Consumer

> Consumer that polls periodically.

```go
class PollingConsumer {
  private running = false;

  constructor(
    private queue: MessageQueue,
    private handler: (msg: any) => Promise<void>,
    private interval: number = 1000,
  ) {}

  start() {
    this.running = true;
    this.poll();
  }

  stop() {
    this.running = false;
  }

  private async poll() {
    while (this.running) {
      try {
        const message = await this.queue.receive({ timeout: this.interval });
        if (message) {
          await this.handler(message);
          await this.queue.ack(message);
        }
      } catch (error) {
        console.error('Polling error:', error);
        await this.delay(this.interval);
      }
    }
  }

  private delay(ms: number) {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }
}
```

**When:** Queues without push, batch processing.
**Related to:** Event-Driven Consumer.

---

### 22. Event-Driven Consumer

> Consumer reactive to events.

```go
class EventDrivenConsumer {
  constructor(
    private channel: MessageChannel,
    private handler: (msg: any) => Promise<void>,
  ) {}

  subscribe(queue: string) {
    this.channel.on('message', async (message) => {
      if (message.queue === queue) {
        try {
          await this.handler(message);
          await this.channel.ack(message);
        } catch (error) {
          await this.channel.nack(message);
        }
      }
    });
  }
}

// With backpressure
class BackpressureConsumer {
  private processing = 0;

  constructor(
    private channel: MessageChannel,
    private handler: (msg: any) => Promise<void>,
    private maxConcurrent: number = 10,
  ) {}

  subscribe(queue: string) {
    this.channel.on('message', async (message) => {
      if (this.processing >= this.maxConcurrent) {
        await this.channel.nack(message, { requeue: true });
        return;
      }

      this.processing++;
      try {
        await this.handler(message);
        await this.channel.ack(message);
      } finally {
        this.processing--;
      }
    });
  }
}
```

**When:** Real-time, reactive, scalable.
**Related to:** Polling Consumer.

---

### 23. Competing Consumers

> Multiple consumers on the same queue.

```go
class CompetingConsumers {
  private consumers: Consumer[] = [];

  constructor(
    private queue: MessageQueue,
    private handler: (msg: any) => Promise<void>,
    private concurrency: number,
  ) {}

  start() {
    for (let i = 0; i < this.concurrency; i++) {
      const consumer = new Consumer(this.queue, this.handler, i);
      consumer.start();
      this.consumers.push(consumer);
    }
  }

  stop() {
    this.consumers.forEach((c) => c.stop());
  }
}

// Each message goes to exactly one consumer
// Queue handles distribution and load balancing
```

**When:** Horizontal scalability, load balancing.
**Related to:** Message Dispatcher.

---

### 24. Message Dispatcher

> Route messages to appropriate handlers.

```go
type MessageHandler = (message: any) => Promise<void>;

class MessageDispatcher {
  private handlers = new Map<string, MessageHandler[]>();

  register(messageType: string, handler: MessageHandler) {
    if (!this.handlers.has(messageType)) {
      this.handlers.set(messageType, []);
    }
    this.handlers.get(messageType)!.push(handler);
  }

  async dispatch(message: { type: string; payload: any }) {
    const handlers = this.handlers.get(message.type) || [];
    await Promise.all(handlers.map((h) => h(message.payload)));
  }
}

// Usage
const dispatcher = new MessageDispatcher();
dispatcher.register('OrderCreated', handleOrderCreated);
dispatcher.register('OrderCreated', sendConfirmationEmail);
dispatcher.register('PaymentReceived', handlePayment);
```

**When:** Command handlers, event handlers.
**Related to:** Observer, Mediator.

---

### 25. Selective Consumer

> Consumer that filters messages.

```go
class SelectiveConsumer {
  constructor(
    private channel: MessageChannel,
    private selector: (msg: any) => boolean,
    private handler: (msg: any) => Promise<void>,
  ) {}

  subscribe(queue: string) {
    this.channel.subscribe(queue, {
      // Some brokers support server-side filtering
      filter: 'header.priority = "high"',
    });

    this.channel.on('message', async (message) => {
      // Client-side filtering for complex logic
      if (!this.selector(message)) {
        await this.channel.ack(message); // Acknowledge but don't process
        return;
      }
      await this.handler(message);
    });
  }
}

// Usage
const highPriorityConsumer = new SelectiveConsumer(
  channel,
  (msg) => msg.priority === 'high',
  handleHighPriority,
);
```

**When:** Message filtering, specialization.
**Related to:** Message Filter.

---

### 26. Durable Subscriber

> Subscription that survives disconnections.

```go
class DurableSubscriber {
  constructor(
    private clientId: string,
    private subscriptionName: string,
  ) {}

  async subscribe(topic: string, handler: (msg: any) => Promise<void>) {
    const subscription = await this.broker.createDurableSubscription({
      clientId: this.clientId,
      subscriptionName: this.subscriptionName,
      topic,
    });

    // Messages are stored even when disconnected
    // On reconnect, receive missed messages

    subscription.on('message', async (msg) => {
      await handler(msg);
      await subscription.ack(msg);
    });
  }
}

// Usage - survives disconnection
const subscriber = new DurableSubscriber('order-service-1', 'order-events');
await subscriber.subscribe('orders.*', handleOrderEvent);
```

**When:** Reliability, offline, recovery.
**Related to:** Guaranteed Delivery.

---

### 27. Idempotent Receiver

> Handler that manages duplicates.

```go
class IdempotentReceiver {
  constructor(
    private processedIds: Set<string> | RedisSet,
    private handler: (msg: any) => Promise<void>,
  ) {}

  async handle(message: { id: string; payload: any }) {
    // Check if already processed
    if (await this.processedIds.has(message.id)) {
      console.log(`Message ${message.id} already processed, skipping`);
      return;
    }

    // Process message
    await this.handler(message.payload);

    // Mark as processed
    await this.processedIds.add(message.id);
  }
}

// With expiration for cleanup
class IdempotentReceiverWithTTL {
  constructor(
    private redis: Redis,
    private ttlSeconds: number = 86400, // 24 hours
  ) {}

  async handle(message: { id: string; payload: any }, handler: Function) {
    const key = `processed:${message.id}`;

    // Try to set key (only succeeds if not exists)
    const wasNew = await this.redis.setnx(key, '1');
    if (!wasNew) {
      return; // Already processed
    }

    await this.redis.expire(key, this.ttlSeconds);
    await handler(message.payload);
  }
}
```

**When:** At-least-once delivery, retries.
**Related to:** Guaranteed Delivery.

---

## Channel Patterns

### 28. Point-to-Point Channel

> A message goes to a single consumer.

```go
class PointToPointChannel {
  private queue: Message[] = [];
  private waiting: ((msg: Message) => void)[] = [];

  send(message: Message) {
    const waiter = this.waiting.shift();
    if (waiter) {
      waiter(message);
    } else {
      this.queue.push(message);
    }
  }

  receive(): Promise<Message> {
    const message = this.queue.shift();
    if (message) {
      return Promise.resolve(message);
    }
    return new Promise((resolve) => {
      this.waiting.push(resolve);
    });
  }
}
```

**When:** Commands, job queues, work distribution.
**Related to:** Publish-Subscribe.

---

### 29. Publish-Subscribe Channel

> A message goes to all subscribers.

```go
class PublishSubscribeChannel {
  private subscribers = new Map<string, ((msg: Message) => void)[]>();

  subscribe(topic: string, handler: (msg: Message) => void) {
    if (!this.subscribers.has(topic)) {
      this.subscribers.set(topic, []);
    }
    this.subscribers.get(topic)!.push(handler);

    return () => {
      const handlers = this.subscribers.get(topic)!;
      const index = handlers.indexOf(handler);
      if (index > -1) handlers.splice(index, 1);
    };
  }

  publish(topic: string, message: Message) {
    const handlers = this.subscribers.get(topic) || [];
    handlers.forEach((handler) => handler(message));

    // Support wildcards
    for (const [pattern, patternHandlers] of this.subscribers) {
      if (this.matchesTopic(pattern, topic)) {
        patternHandlers.forEach((h) => h(message));
      }
    }
  }

  private matchesTopic(pattern: string, topic: string): boolean {
    // orders.* matches orders.created, orders.deleted
    // orders.# matches orders.created.success
    const regex = pattern.replace(/\*/g, '[^.]+').replace(/#/g, '.+');
    return new RegExp(`^${regex}$`).test(topic);
  }
}
```

**When:** Events, notifications, broadcasting.
**Related to:** Observer.

---

### 30. Dead Letter Channel

> Queue for unprocessable messages.

```go
class DeadLetterChannel {
  constructor(private dlq: MessageQueue) {}

  async sendToDeadLetter(
    message: any,
    error: Error,
    attempts: number,
  ) {
    await this.dlq.send({
      originalMessage: message,
      error: {
        name: error.name,
        message: error.message,
        stack: error.stack,
      },
      failedAt: new Date(),
      attempts,
    });
  }
}

class MessageProcessor {
  constructor(
    private handler: (msg: any) => Promise<void>,
    private dlc: DeadLetterChannel,
    private maxRetries: number = 3,
  ) {}

  async process(message: any) {
    let attempts = 0;

    while (attempts < this.maxRetries) {
      try {
        await this.handler(message);
        return;
      } catch (error) {
        attempts++;
        if (attempts >= this.maxRetries) {
          await this.dlc.sendToDeadLetter(message, error, attempts);
        }
      }
    }
  }
}
```

**When:** Error handling, debugging, retry exhaust.
**Related to:** Guaranteed Delivery.

---

### 31. Guaranteed Delivery

> Ensure message delivery.

```go
class GuaranteedDelivery {
  constructor(
    private store: MessageStore,
    private channel: MessageChannel,
  ) {}

  async send(destination: string, message: any) {
    const id = crypto.randomUUID();

    // 1. Persist before sending
    await this.store.save({
      id,
      destination,
      message,
      status: 'pending',
      createdAt: new Date(),
    });

    try {
      // 2. Send message
      await this.channel.send(destination, { id, ...message });

      // 3. Mark as sent (or wait for ack)
      await this.store.updateStatus(id, 'sent');
    } catch (error) {
      // Will be retried by recovery process
      await this.store.updateStatus(id, 'failed');
      throw error;
    }
  }

  // Recovery process for failed messages
  async recoverPendingMessages() {
    const pending = await this.store.findByStatus('pending', 'failed');
    for (const msg of pending) {
      await this.send(msg.destination, msg.message);
    }
  }
}
```

**When:** Critical reliability, transactions.
**Related to:** Outbox Pattern, Transactional Messaging.

---

## Decision Table

| Need | Pattern |
|--------|---------|
| Action to execute | Command Message |
| Notification of fact | Event Message |
| Request/response | Request-Reply |
| Link messages | Correlation Identifier |
| Route dynamically | Content-Based Router |
| Filter messages | Message Filter |
| Send to multiple | Recipient List |
| Split message | Splitter |
| Combine messages | Aggregator |
| Compare sources | Scatter-Gather |
| Dynamic workflow | Routing Slip |
| Orchestration | Process Manager |
| Convert format | Message Translator |
| Add metadata | Envelope Wrapper |
| Enrich data | Content Enricher |
| Batch processing | Polling Consumer |
| Reactive | Event-Driven Consumer |
| Horizontal scaling | Competing Consumers |
| Route handlers | Message Dispatcher |
| Filter on reception | Selective Consumer |
| Survive disconnection | Durable Subscriber |
| Handle duplicates | Idempotent Receiver |
| Single recipient | Point-to-Point |
| Broadcast | Publish-Subscribe |
| Errors | Dead Letter Channel |
| Reliability | Guaranteed Delivery |

## Sources

- [Enterprise Integration Patterns](https://www.enterpriseintegrationpatterns.com/)
- [Gregor Hohpe - EIP Book](https://www.amazon.com/Enterprise-Integration-Patterns-Designing-Deploying/dp/0321200683)
