# [Pattern Name]

> [Short one-line description - what the pattern does]

---

## Principle

[Explanation of the concept in 2-3 paragraphs]

```
[ASCII diagram if applicable]

┌─────────┐         ┌─────────┐
│ Client  │ ──────► │ Pattern │
└─────────┘         └─────────┘
```

---

## Problem Solved

[What problem does this pattern solve?]

- Problem 1
- Problem 2
- Problem 3

---

## Solution

[How does the pattern solve this problem?]

```go
// [PatternInterface] defines the main abstraction.
type [PatternInterface] interface {
	[Method]() ([ReturnType], error)
}

// [ConcreteImplementation] implements [PatternInterface].
type [ConcreteImplementation] struct {
	// fields
}

// [Method] implements the pattern logic.
func (c *[ConcreteImplementation]) [Method]() ([ReturnType], error) {
	// Pattern logic
	return [value], nil
}

// New[ConcreteImplementation] creates a new instance.
func New[ConcreteImplementation]() *[ConcreteImplementation] {
	return &[ConcreteImplementation]{}
}

// Usage:
// instance := New[ConcreteImplementation]()
// result, err := instance.[Method]()
```

---

## Complete Example

```go
// Realistic and functional example

// 1. Definition
[complete code]

// 2. Usage
[usage example]

// 3. Expected output
// > [result]
```

---

## Variants

| Variant | Description | Use Case |
|---------|-------------|----------|
| [Variant1] | [Description] | [When to use] |
| [Variant2] | [Description] | [When to use] |

---

## When to Use

- ✅ [Use case 1]
- ✅ [Use case 2]
- ✅ [Use case 3]

## When NOT to Use

- ❌ [Anti-case 1]
- ❌ [Anti-case 2]
- ❌ [Anti-case 3]

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|------------|---------------|
| [Advantage 1] | [Disadvantage 1] |
| [Advantage 2] | [Disadvantage 2] |
| [Advantage 3] | [Disadvantage 3] |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| [Pattern1] | [Complementary / Alternative / Composable with] |
| [Pattern2] | [Often used together] |
| [Pattern3] | [Similar but for X] |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| [Framework1] | [How it's implemented] |
| [Framework2] | [How it's implemented] |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| [Anti1] | [What's wrong] | [How to fix] |
| [Anti2] | [What's wrong] | [How to fix] |

---

## Tests

```go
func Test[PatternName](t *testing.T) {
	tests := []struct {
		name     string
		input    [InputType]
		expected [ExpectedType]
		wantErr  bool
	}{
		{
			name:     "[test case description]",
			input:    [testInput],
			expected: [expectedValue],
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := [functionUnderTest](tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("got error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}
```

---

## Sources

- [Source Title 1](https://url1)
- [Source Title 2](https://url2)
- [Reference book/article]
