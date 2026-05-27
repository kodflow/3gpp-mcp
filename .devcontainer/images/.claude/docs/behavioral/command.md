# Command Pattern

> Encapsulate a request as an object to parameterize, log, or undo.

## Intent

Encapsulate a request as an object, allowing parameterization of clients
with different requests, queue, log requests,
and support reversible operations (undo).

## Structure

```go
package main

import "fmt"

// Command is the interface for executing and undoing commands.
type Command interface {
	Execute() error
	Undo() error
}

// TextEditor is the receiver that performs actions.
type TextEditor struct {
	content        string
	cursorPosition int
}

// GetContent returns the editor content.
func (e *TextEditor) GetContent() string {
	return e.content
}

// InsertAt inserts text at the given position.
func (e *TextEditor) InsertAt(position int, text string) {
	e.content = e.content[:position] + text + e.content[position:]
	e.cursorPosition = position + len(text)
}

// DeleteRange deletes text in the given range and returns it.
func (e *TextEditor) DeleteRange(start, end int) string {
	if end > len(e.content) {
		end = len(e.content)
	}
	deleted:= e.content[start:end]
	e.content = e.content[:start] + e.content[end:]
	e.cursorPosition = start
	return deleted
}

// GetCursor returns the current cursor position.
func (e *TextEditor) GetCursor() int {
	return e.cursorPosition
}

// SetCursor sets the cursor position.
func (e *TextEditor) SetCursor(position int) {
	if position > len(e.content) {
		position = len(e.content)
	}
	if position < 0 {
		position = 0
	}
	e.cursorPosition = position
}

// InsertTextCommand inserts text at the cursor.
type InsertTextCommand struct {
	editor   *TextEditor
	text     string
	position int
}

// NewInsertTextCommand creates a new insert command.
func NewInsertTextCommand(editor *TextEditor, text string) *InsertTextCommand {
	return &InsertTextCommand{
		editor:   editor,
		text:     text,
		position: editor.GetCursor(),
	}
}

// Execute inserts the text.
func (c *InsertTextCommand) Execute() error {
	c.editor.InsertAt(c.position, c.text)
	return nil
}

// Undo removes the inserted text.
func (c *InsertTextCommand) Undo() error {
	c.editor.DeleteRange(c.position, c.position+len(c.text))
	return nil
}

// DeleteTextCommand deletes text at the cursor.
type DeleteTextCommand struct {
	editor      *TextEditor
	length      int
	position    int
	deletedText string
}

// NewDeleteTextCommand creates a new delete command.
func NewDeleteTextCommand(editor *TextEditor, length int) *DeleteTextCommand {
	return &DeleteTextCommand{
		editor:   editor,
		length:   length,
		position: editor.GetCursor(),
	}
}

// Execute deletes the text.
func (c *DeleteTextCommand) Execute() error {
	c.deletedText = c.editor.DeleteRange(c.position, c.position+c.length)
	return nil
}

// Undo restores the deleted text.
func (c *DeleteTextCommand) Undo() error {
	c.editor.InsertAt(c.position, c.deletedText)
	return nil
}

// CommandHistory manages undo/redo stacks.
type CommandHistory struct {
	undoStack []Command
	redoStack []Command
}

// Execute executes a command and adds it to history.
func (h *CommandHistory) Execute(command Command) error {
	if err:= command.Execute(); err != nil {
		return err
	}
	h.undoStack = append(h.undoStack, command)
	h.redoStack = nil // Clear redo after new action
	return nil
}

// Undo undoes the last command.
func (h *CommandHistory) Undo() error {
	if len(h.undoStack) == 0 {
		return fmt.Errorf("nothing to undo")
	}

	command:= h.undoStack[len(h.undoStack)-1]
	h.undoStack = h.undoStack[:len(h.undoStack)-1]

	if err:= command.Undo(); err != nil {
		return err
	}

	h.redoStack = append(h.redoStack, command)
	return nil
}

// Redo redoes the last undone command.
func (h *CommandHistory) Redo() error {
	if len(h.redoStack) == 0 {
		return fmt.Errorf("nothing to redo")
	}

	command:= h.redoStack[len(h.redoStack)-1]
	h.redoStack = h.redoStack[:len(h.redoStack)-1]

	if err:= command.Execute(); err != nil {
		return err
	}

	h.undoStack = append(h.undoStack, command)
	return nil
}

// CanUndo returns true if undo is possible.
func (h *CommandHistory) CanUndo() bool {
	return len(h.undoStack) > 0
}

// CanRedo returns true if redo is possible.
func (h *CommandHistory) CanRedo() bool {
	return len(h.redoStack) > 0
}
```

## Usage

```go
func main() {
	editor:= &TextEditor{}
	history:= &CommandHistory{}

	// Execute commands
	history.Execute(NewInsertTextCommand(editor, "Hello"))
	history.Execute(NewInsertTextCommand(editor, " World"))
	fmt.Println(editor.GetContent()) // "Hello World"

	// Undo
	history.Undo()
	fmt.Println(editor.GetContent()) // "Hello"

	// Redo
	history.Redo()
	fmt.Println(editor.GetContent()) // "Hello World"

	// New action clears redo
	history.Execute(NewInsertTextCommand(editor, "!"))
	fmt.Println(editor.GetContent())  // "Hello World!"
	fmt.Println(history.CanRedo())    // false
}
```

## Macro Command (Composite)

```go
// MacroCommand executes multiple commands as one.
type MacroCommand struct {
	commands []Command
}

// NewMacroCommand creates a new macro command.
func NewMacroCommand() *MacroCommand {
	return &MacroCommand{}
}

// Add adds a command to the macro.
func (m *MacroCommand) Add(command Command) {
	m.commands = append(m.commands, command)
}

// Execute executes all commands in order.
func (m *MacroCommand) Execute() error {
	for _, command:= range m.commands {
		if err:= command.Execute(); err != nil {
			return err
		}
	}
	return nil
}

// Undo undoes all commands in reverse order.
func (m *MacroCommand) Undo() error {
	for i:= len(m.commands) - 1; i >= 0; i-- {
		if err:= m.commands[i].Undo(); err != nil {
			return err
		}
	}
	return nil
}

// Usage - format a text block
func macroExample() {
	editor:= &TextEditor{}
	history:= &CommandHistory{}

	formatMacro:= NewMacroCommand()
	// formatMacro.Add(NewSelectAllCommand(editor))
	// formatMacro.Add(NewUppercaseCommand(editor))
	// formatMacro.Add(NewBoldCommand(editor))

	history.Execute(formatMacro)
	// Everything is undone in a single undo operation
	history.Undo()
}
```

## Command Queue (Asynchronous)

```go
import "context"

// AsyncCommand is a command that can execute asynchronously.
type AsyncCommand interface {
	Execute(ctx context.Context) error
	Undo(ctx context.Context) error
}

// CommandQueue manages async command execution.
type CommandQueue struct {
	queue        []AsyncCommand
	isProcessing bool
}

// Enqueue adds a command to the queue and processes it.
func (q *CommandQueue) Enqueue(ctx context.Context, command AsyncCommand) error {
	q.queue = append(q.queue, command)
	return q.processQueue(ctx)
}

func (q *CommandQueue) processQueue(ctx context.Context) error {
	if q.isProcessing {
		return nil
	}
	q.isProcessing = true
	defer func() { q.isProcessing = false }()

	for len(q.queue) > 0 {
		command:= q.queue[0]
		q.queue = q.queue[1:]

		if err:= command.Execute(ctx); err != nil {
			fmt.Printf("Command failed: %v\n", err)
			// Optional: rollback previous commands
		}
	}

	return nil
}

// EmailService is a mock email service.
type EmailService interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SendEmailCommand sends an email asynchronously.
type SendEmailCommand struct {
	emailService EmailService
	to           string
	subject      string
	body         string
}

// NewSendEmailCommand creates a new email command.
func NewSendEmailCommand(emailService EmailService, to, subject, body string) *SendEmailCommand {
	return &SendEmailCommand{
		emailService: emailService,
		to:           to,
		subject:      subject,
		body:         body,
	}
}

// Execute sends the email.
func (c *SendEmailCommand) Execute(ctx context.Context) error {
	return c.emailService.Send(ctx, c.to, c.subject, c.body)
}

// Undo sends a cancellation email.
func (c *SendEmailCommand) Undo(ctx context.Context) error {
	// Emails cannot be undone, but we can send a follow-up
	return c.emailService.Send(
		ctx,
		c.to,
		"[CANCELLED] "+c.subject,
		"Please disregard the previous email.",
	)
}
```

## Transactional Command

```go
// TransactionalCommand supports validation, commit, and rollback.
type TransactionalCommand interface {
	Command
	Validate() error
	Commit() error
	Rollback() error
}

// TransactionManager manages transactional commands.
type TransactionManager struct {
	commands []TransactionalCommand
	executed []TransactionalCommand
}

// Add adds a command to the transaction.
func (t *TransactionManager) Add(command TransactionalCommand) {
	t.commands = append(t.commands, command)
}

// ExecuteAll executes all commands in a transaction.
func (t *TransactionManager) ExecuteAll() error {
	// Validation phase
	for _, command:= range t.commands {
		if err:= command.Validate(); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}

	// Execution phase
	for _, command:= range t.commands {
		if err:= command.Execute(); err != nil {
			// Rollback phase
			for i:= len(t.executed) - 1; i >= 0; i-- {
				t.executed[i].Rollback()
			}
			return fmt.Errorf("execution failed: %w", err)
		}
		t.executed = append(t.executed, command)
	}

	// Commit phase
	for _, command:= range t.executed {
		if err:= command.Commit(); err != nil {
			return fmt.Errorf("commit failed: %w", err)
		}
	}

	return nil
}
```

## Anti-patterns

```go
// BAD: Command that does too much
type GodCommand struct{}

func (c *GodCommand) Execute() error {
	// Should be multiple commands
	c.validateInput()
	c.processData()
	c.saveToDatabase()
	c.sendNotification()
	c.updateCache()
	return nil
}

func (c *GodCommand) Undo() error {
	// How to undo all this properly?
	return nil
}

func (c *GodCommand) validateInput()    {}
func (c *GodCommand) processData()      {}
func (c *GodCommand) saveToDatabase()   {}
func (c *GodCommand) sendNotification() {}
func (c *GodCommand) updateCache()      {}

// BAD: Command with external state
type StatefulCommand struct {
	lastResult interface{} // Etat partage = problemes
}

func (c *StatefulCommand) Execute() error {
	c.lastResult = c.doSomething()
	return nil
}

func (c *StatefulCommand) Undo() error {
	return nil
}

func (c *StatefulCommand) doSomething() interface{} {
	return nil
}

// BAD: Incomplete undo
type IncompleteUndoCommand struct {
	previousState *State
}

type State struct{}

func (c *IncompleteUndoCommand) Execute() error {
	// Forgot to save state before modification
	c.modify()
	return nil
}

func (c *IncompleteUndoCommand) Undo() error {
	// previousState is nil!
	c.restore(c.previousState)
	return nil
}

func (c *IncompleteUndoCommand) modify()          {}
func (c *IncompleteUndoCommand) restore(s *State) {}
```

## Unit Tests

```go
package main

import (
	"testing"
)

func TestInsertTextCommand(t *testing.T) {
	t.Run("should insert text at cursor", func(t *testing.T) {
		editor:= &TextEditor{}
		history:= &CommandHistory{}

		history.Execute(NewInsertTextCommand(editor, "Hello"))

		if editor.GetContent() != "Hello" {
			t.Errorf("expected 'Hello', got '%s'", editor.GetContent())
		}
	})

	t.Run("should support undo", func(t *testing.T) {
		editor:= &TextEditor{}
		history:= &CommandHistory{}

		history.Execute(NewInsertTextCommand(editor, "Hello"))
		history.Undo()

		if editor.GetContent() != "" {
			t.Errorf("expected empty string, got '%s'", editor.GetContent())
		}
	})
}

func TestDeleteTextCommand(t *testing.T) {
	t.Run("should delete text", func(t *testing.T) {
		editor:= &TextEditor{}
		history:= &CommandHistory{}

		history.Execute(NewInsertTextCommand(editor, "Hello World"))
		editor.SetCursor(5)
		history.Execute(NewDeleteTextCommand(editor, 6))

		if editor.GetContent() != "Hello" {
			t.Errorf("expected 'Hello', got '%s'", editor.GetContent())
		}
	})

	t.Run("should restore deleted text on undo", func(t *testing.T) {
		editor:= &TextEditor{}
		history:= &CommandHistory{}

		history.Execute(NewInsertTextCommand(editor, "Hello World"))
		editor.SetCursor(5)
		history.Execute(NewDeleteTextCommand(editor, 6))
		history.Undo()

		if editor.GetContent() != "Hello World" {
			t.Errorf("expected 'Hello World', got '%s'", editor.GetContent())
		}
	})
}

func TestCommandHistory(t *testing.T) {
	t.Run("should support multiple undo/redo", func(t *testing.T) {
		editor:= &TextEditor{}
		history:= &CommandHistory{}

		history.Execute(NewInsertTextCommand(editor, "A"))
		history.Execute(NewInsertTextCommand(editor, "B"))
		history.Execute(NewInsertTextCommand(editor, "C"))

		if editor.GetContent() != "ABC" {
			t.Errorf("expected 'ABC', got '%s'", editor.GetContent())
		}

		history.Undo()
		if editor.GetContent() != "AB" {
			t.Errorf("expected 'AB', got '%s'", editor.GetContent())
		}

		history.Undo()
		if editor.GetContent() != "A" {
			t.Errorf("expected 'A', got '%s'", editor.GetContent())
		}

		history.Redo()
		if editor.GetContent() != "AB" {
			t.Errorf("expected 'AB', got '%s'", editor.GetContent())
		}
	})

	t.Run("should clear redo stack after new command", func(t *testing.T) {
		editor:= &TextEditor{}
		history:= &CommandHistory{}

		history.Execute(NewInsertTextCommand(editor, "A"))
		history.Undo()
		history.Execute(NewInsertTextCommand(editor, "B"))

		if history.CanRedo() {
			t.Error("redo should not be available after new command")
		}
	})
}

func TestMacroCommand(t *testing.T) {
	t.Run("should execute all commands", func(t *testing.T) {
		editor:= &TextEditor{}

		macro:= NewMacroCommand()
		macro.Add(NewInsertTextCommand(editor, "Hello"))
		macro.Add(NewInsertTextCommand(editor, " World"))

		macro.Execute()

		if editor.GetContent() != "Hello World" {
			t.Errorf("expected 'Hello World', got '%s'", editor.GetContent())
		}
	})

	t.Run("should undo all commands in reverse order", func(t *testing.T) {
		editor:= &TextEditor{}
		history:= &CommandHistory{}

		macro:= NewMacroCommand()
		macro.Add(NewInsertTextCommand(editor, "Hello"))
		macro.Add(NewInsertTextCommand(editor, " World"))

		history.Execute(macro)
		history.Undo()

		if editor.GetContent() != "" {
			t.Errorf("expected empty string, got '%s'", editor.GetContent())
		}
	})
}
```

## When to Use

- Reversible operations (Undo/Redo)
- Request queuing
- Operation logging
- Transactions
- Structured callbacks

## Related Patterns

- **Memento**: Saves state for undo
- **Strategy**: Algorithms vs operations
- **Composite**: Macro commands

## Sources

- [Refactoring Guru - Command](https://refactoring.guru/design-patterns/command)
