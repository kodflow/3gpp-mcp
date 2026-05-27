# Behavioral Patterns (GoF)

Communication patterns between objects.

## Detailed Files

| Pattern | Fichier | Description |
|---------|---------|-------------|
| Chain of Responsibility | [chain-of-responsibility.md](chain-of-responsibility.md) | Middleware pattern |
| Command | [command.md](command.md) | Undo/Redo, transactions |
| Interpreter | [interpreter.md](interpreter.md) | Interpret a grammar/DSL |
| Iterator | [iterator.md](iterator.md) | Traverse without exposing structure |
| Mediator | [mediator.md](mediator.md) | Reduce direct dependencies |
| Memento | [memento.md](memento.md) | Save and restore state |
| Observer | [observer.md](observer.md) | Modern Event Emitter |
| State | [state.md](state.md) | State machine pattern |
| Strategy | [strategy.md](strategy.md) | Interchangeable algorithms |
| Template Method | [template-method.md](template-method.md) | Algorithm skeleton with variations |
| Visitor | [visitor.md](visitor.md) | Operations on an object structure |

## The 11 Patterns

### 1. Chain of Responsibility

> Chain of handlers that pass the request.

See detailed file: [chain-of-responsibility.md](chain-of-responsibility.md)

```go
const chain = new AuthHandler();
chain.setNext(new ValidationHandler()).setNext(new BusinessHandler());
chain.handle(request);
```

**When:** Middleware, validations, filters.

---

### 2. Command

> Encapsulate a request as an object.

See detailed file: [command.md](command.md)

```go
interface Command {
  execute(): void;
  undo(): void;
}

class CommandInvoker {
  private history: Command[] = [];

  execute(command: Command) {
    command.execute();
    this.history.push(command);
  }

  undo() {
    this.history.pop()?.undo();
  }
}
```

**When:** Undo/redo, queues, transactions.

---

### 3. Iterator

> Traverse without exposing internal structure.

```go
interface Iterator<T> {
  next(): T | null;
  hasNext(): boolean;
}

class TreeIterator<T> implements Iterator<T> {
  private stack: TreeNode<T>[] = [];

  constructor(root: TreeNode<T>) {
    this.stack.push(root);
  }

  next(): T | null {
    if (!this.hasNext()) return null;
    const node = this.stack.pop()!;
    if (node.right) this.stack.push(node.right);
    if (node.left) this.stack.push(node.left);
    return node.value;
  }
}
```

**When:** Collections custom, lazy loading.

---

### 4. Mediator

> Reduce direct dependencies between components.

```go
interface Mediator {
  notify(sender: Component, event: string): void;
}

class DialogMediator implements Mediator {
  notify(sender: Component, event: string) {
    if (sender === this.submitBtn && event === 'click') {
      if (this.form.validate()) this.form.submit();
    }
  }
}
```

**When:** Complex UIs, systems with many interactions.

---

### 5. Memento

> Save and restore state.

```go
class Editor {
  save(): EditorMemento {
    return new EditorMemento(this.content);
  }

  restore(memento: EditorMemento) {
    this.content = memento.getState();
  }
}
```

**When:** Undo, snapshots, checkpoints.

---

### 6. Observer

> Notification of changes.

See detailed file: [observer.md](observer.md)

```go
class TypedEventEmitter<Events extends EventMap> {
  on<K extends keyof Events>(event: K, callback: (data: Events[K]) => void) {
    // ...
  }

  emit<K extends keyof Events>(event: K, data: Events[K]) {
    // ...
  }
}
```

**When:** Events, reactive programming, UI updates.

---

### 7. State

> Behavior that changes according to state.

See detailed file: [state.md](state.md)

```go
class Order {
  private state: OrderState;

  setState(state: OrderState) { this.state = state; }
  confirm() { this.state.confirm(this); }
  ship() { this.state.ship(this); }
}
```

**When:** State machines, workflows.

---

### 8. Strategy

> Interchangeable algorithms.

See detailed file: [strategy.md](strategy.md)

```go
class PaymentProcessor {
  constructor(private strategy: PaymentStrategy) {}

  setStrategy(strategy: PaymentStrategy) {
    this.strategy = strategy;
  }

  async checkout(amount: number) {
    return this.strategy.pay(amount);
  }
}
```

**When:** Multiple algorithms, runtime selection.

---

### 9. Template Method

> Algorithm skeleton, details in subclasses.

```go
abstract class DataMiner {
  mine(path: string) {
    const data = this.openFile(path);
    const parsed = this.parse(data);
    const analyzed = this.analyze(parsed);
    this.report(analyzed);
  }

  abstract openFile(path: string): string;
  abstract parse(data: string): object;
  analyze(data: object) { return data; }
}
```

**When:** Common algorithm, variable steps.

---

### 10. Visitor

> Operations on an object structure.

```go
interface Visitor {
  visitCircle(c: Circle): void;
  visitRectangle(r: Rectangle): void;
}

class AreaCalculator implements Visitor {
  visitCircle(c: Circle) { return Math.PI * c.radius ** 2; }
  visitRectangle(r: Rectangle) { return r.width * r.height; }
}
```

**When:** Various operations on stable structures.

---

### 11. Interpreter

> Interpret a grammar.

```go
interface Expression {
  interpret(context: Map<string, number>): number;
}

class AddExpression implements Expression {
  constructor(private left: Expression, private right: Expression) {}
  interpret(ctx: Map<string, number>) {
    return this.left.interpret(ctx) + this.right.interpret(ctx);
  }
}
```

**When:** DSL, rules, expressions (rarely used).

---

## Decision Table

| Need | Pattern |
|--------|---------|
| Pipeline of handlers | Chain of Responsibility |
| Undo/redo, queue | Command |
| Custom traversal | Iterator |
| Reduce UI coupling | Mediator |
| State snapshot | Memento |
| Events, reactive | Observer |
| State machine | State |
| Variable algorithms | Strategy |
| Skeleton + variations | Template Method |
| Operations on structure | Visitor |

## Sources

- [Refactoring Guru - Behavioral Patterns](https://refactoring.guru/design-patterns/behavioral-patterns)
