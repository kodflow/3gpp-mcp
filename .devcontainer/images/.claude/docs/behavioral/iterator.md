# Iterator

> Access elements of a collection without exposing its internal structure.

---

## Principle

The Iterator pattern allows traversing a collection without knowing its
underlying structure (list, tree, graph).
Go natively uses this pattern with `range` and channels.

```text
┌────────────────┐      ┌────────────────┐
│   Collection   │─────▶│    Iterator    │
│  (CreateIter)  │      │ (Next, HasNext)│
└────────────────┘      └────────────────┘
         │                      │
         ▼                      ▼
┌────────────────┐      ┌────────────────┐
│ConcreteCollect │      │ ConcreteIter   │
└────────────────┘      └────────────────┘
```

---

## Problem Solved

- Traverse a collection without knowing its implementation
- Support multiple simultaneous traversals
- Provide a uniform interface for different structures
- Separate traversal logic from the collection

---

## Solution

```go
package main

import "fmt"

// Iterator defines the traversal interface.
type Iterator[T any] interface {
    HasNext() bool
    Next() T
}

// Collection defines the collection interface.
type Collection[T any] interface {
    CreateIterator() Iterator[T]
}

// SliceCollection is a slice-based collection.
type SliceCollection[T any] struct {
    items []T
}

func NewSliceCollection[T any](items ...T) *SliceCollection[T] {
    return &SliceCollection[T]{items: items}
}

func (c *SliceCollection[T]) CreateIterator() Iterator[T] {
    return &SliceIterator[T]{collection: c, index: 0}
}

// SliceIterator traverses a slice.
type SliceIterator[T any] struct {
    collection *SliceCollection[T]
    index      int
}

func (i *SliceIterator[T]) HasNext() bool {
    return i.index < len(i.collection.items)
}

func (i *SliceIterator[T]) Next() T {
    if i.HasNext() {
        item:= i.collection.items[i.index]
        i.index++
        return item
    }
    var zero T
    return zero
}

// Usage:
// coll:= NewSliceCollection(1, 2, 3, 4, 5)
// iter:= coll.CreateIterator()
// for iter.HasNext() {
//     fmt.Println(iter.Next())
// }
```

---

## Complete Example

```go
package main

import (
    "fmt"
    "iter"
)

// Book represents a book.
type Book struct {
    Title  string
    Author string
    Year   int
}

// Library is a collection of books.
type Library struct {
    books []*Book
}

func NewLibrary() *Library {
    return &Library{books: make([]*Book, 0)}
}

func (l *Library) Add(book *Book) {
    l.books = append(l.books, book)
}

// All returns a Go 1.23+ iterator (iter.Seq).
func (l *Library) All() iter.Seq[*Book] {
    return func(yield func(*Book) bool) {
        for _, book:= range l.books {
            if !yield(book) {
                return
            }
        }
    }
}

// ByAuthor returns an iterator filtered by author.
func (l *Library) ByAuthor(author string) iter.Seq[*Book] {
    return func(yield func(*Book) bool) {
        for _, book:= range l.books {
            if book.Author == author {
                if !yield(book) {
                    return
                }
            }
        }
    }
}

// ByYearRange returns books within a year range.
func (l *Library) ByYearRange(from, to int) iter.Seq[*Book] {
    return func(yield func(*Book) bool) {
        for _, book:= range l.books {
            if book.Year >= from && book.Year <= to {
                if !yield(book) {
                    return
                }
            }
        }
    }
}

// Channel-based iterator (pre Go 1.23 approach)
func (l *Library) Chan() <-chan *Book {
    ch:= make(chan *Book)
    go func() {
        defer close(ch)
        for _, book:= range l.books {
            ch <- book
        }
    }()
    return ch
}

func main() {
    library:= NewLibrary()
    library.Add(&Book{Title: "1984", Author: "George Orwell", Year: 1949})
    library.Add(&Book{Title: "Brave New World", Author: "Aldous Huxley", Year: 1932})
    library.Add(&Book{Title: "Animal Farm", Author: "George Orwell", Year: 1945})
    library.Add(&Book{Title: "Fahrenheit 451", Author: "Ray Bradbury", Year: 1953})

    // Iterate over all books (Go 1.23+)
    fmt.Println("All books:")
    for book:= range library.All() {
        fmt.Printf("  - %s (%d)\n", book.Title, book.Year)
    }

    // Filter by author
    fmt.Println("\nBooks by George Orwell:")
    for book:= range library.ByAuthor("George Orwell") {
        fmt.Printf("  - %s (%d)\n", book.Title, book.Year)
    }

    // Filter by year
    fmt.Println("\nBooks from 1940-1950:")
    for book:= range library.ByYearRange(1940, 1950) {
        fmt.Printf("  - %s (%d)\n", book.Title, book.Year)
    }

    // Channel-based (pre Go 1.23)
    fmt.Println("\nUsing channel iterator:")
    for book:= range library.Chan() {
        fmt.Printf("  - %s\n", book.Title)
    }
}
```

---

## Variants

| Variant | Description | Use Case |
|----------|-------------|-------------|
| Forward Iterator | Forward traversal | Standard case |
| Reverse Iterator | Reverse traversal | History, undo |
| Filter Iterator | Filters elements | Complex queries |
| Transform Iterator | Transforms while traversing | Map/Select |

---

## When to Use

- Traverse a collection without exposing its structure
- Support multiple simultaneous traversals
- Provide different traversal strategies
- Decouple algorithms from collections

## When NOT to Use

- Simple collections (use range directly)
- Only one type of traversal needed
- Performance critical (abstraction overhead)

---

## Advantages / Disadvantages

| Advantages | Disadvantages |
|-----------|---------------|
| Single Responsibility | Overhead for simple collections |
| Open/Closed Principle | Added complexity |
| Parallel traversals | Go already has range and channels |
| Lazy iterators | |

---

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Composite | Iterator can traverse composites |
| Factory Method | Create iterators |
| Memento | Iterator can save its position |
| Visitor | Alternative: Visitor iterates, Iterator traverses |

---

## Framework Implementations

| Framework/Lib | Implementation |
|---------------|----------------|
| iter (Go 1.23+) | iter.Seq, iter.Seq2 |
| channels | Concurrent-safe iterators |
| bufio.Scanner | Iterator over lines/tokens |

---

## Anti-patterns to Avoid

| Anti-pattern | Problem | Solution |
|--------------|----------|----------|
| Mutable iterator | Shared state | Create new iterator |
| Forgetting close | Resource leak (channels) | defer close() |
| Modification during iteration | Undefined behavior | Copy or lock |

---

## Tests

```go
func TestSliceIterator(t *testing.T) {
    coll:= NewSliceCollection(1, 2, 3)
    iter:= coll.CreateIterator()

    var result []int
    for iter.HasNext() {
        result = append(result, iter.Next())
    }

    expected:= []int{1, 2, 3}
    if !reflect.DeepEqual(result, expected) {
        t.Errorf("expected %v, got %v", expected, result)
    }
}

func TestLibrary_ByAuthor(t *testing.T) {
    library:= NewLibrary()
    library.Add(&Book{Title: "Book1", Author: "A", Year: 2000})
    library.Add(&Book{Title: "Book2", Author: "B", Year: 2001})
    library.Add(&Book{Title: "Book3", Author: "A", Year: 2002})

    var count int
    for range library.ByAuthor("A") {
        count++
    }

    if count != 2 {
        t.Errorf("expected 2 books by A, got %d", count)
    }
}

func TestIterator_Empty(t *testing.T) {
    coll:= NewSliceCollection[int]()
    iter:= coll.CreateIterator()

    if iter.HasNext() {
        t.Error("expected empty iterator")
    }
}
```

---

## Sources

- [Refactoring Guru - Iterator](https://refactoring.guru/design-patterns/iterator)
- [Gang of Four - Design Patterns](https://en.wikipedia.org/wiki/Design_Patterns)
- [Go 1.23 iter package](https://pkg.go.dev/iter)
