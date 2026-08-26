package goal

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// StepLog is one attempt's log file. Every step gets its own, timestamped, so a
// failure remains diagnosable long after the process is gone — the mission's
// "do not replace a useful diagnosis with 'command failed'".
type StepLog struct {
	f    *os.File
	path string
	mu   sync.Mutex
	tail []string // last lines, for the error message
}

// NewStepLog opens .local/logs/<UTC timestamp>-<step>.log.
func NewStepLog(local, step string) (*StepLog, error) {
	dir := filepath.Join(local, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%s-%s.log", time.Now().UTC().Format("20060102T150405Z"), sanitiseName(step))
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		return nil, err
	}
	return &StepLog{f: f, path: p}, nil
}

func (l *StepLog) Path() string { return l.path }

func (l *StepLog) Close() {
	if l != nil && l.f != nil {
		_ = l.f.Sync()
		_ = l.f.Close()
	}
}

// Printf writes a line to the log and echoes it to stderr, so a long run is
// observable while it happens instead of only after it ends.
func (l *StepLog) Printf(format string, a ...any) {
	if l == nil {
		return
	}
	msg := fmt.Sprintf(format, a...)
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.f, "%s | %s\n", time.Now().UTC().Format("15:04:05"), msg)
	fmt.Fprintf(os.Stderr, "  \033[2m%s\033[0m\n", msg)
}

func (l *StepLog) writeLine(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.f, s)
	l.tail = append(l.tail, s)
	if len(l.tail) > 40 {
		l.tail = l.tail[len(l.tail)-40:]
	}
}

// Tail returns the last captured lines, used to build an informative error.
func (l *StepLog) Tail(n int) string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.tail
	if len(t) > n {
		t = t[len(t)-n:]
	}
	return strings.Join(t, "\n")
}

// Cmd describes an external command to run as part of a step.
type Cmd struct {
	Name string
	Args []string
	Dir  string
	Env  []string
	// Echo streams the child's output to stderr as well as to the log. Use it
	// for long steps whose progress the operator needs to see (conversion,
	// embedding); leave it off for chatty, uninteresting commands.
	Echo bool
}

// ExecError carries everything needed to diagnose a failed command without
// re-running it: what was run, where, the exit code, and the tail of its output.
type ExecError struct {
	Cmd      string
	Dir      string
	ExitCode int
	Tail     string
	Err      error
}

func (e *ExecError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "command failed (exit %d): %s", e.ExitCode, e.Cmd)
	if e.Dir != "" {
		fmt.Fprintf(&b, "\n  cwd: %s", e.Dir)
	}
	if e.Tail != "" {
		fmt.Fprintf(&b, "\n  output tail:\n%s", indent(e.Tail, "    "))
	}
	return b.String()
}

func (e *ExecError) Unwrap() error { return e.Err }

func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// Run executes a command, capturing stdout and stderr into the step log.
//
// There is deliberately no "ignore failure" option: a step that legitimately
// tolerates a failure declares Optional at the STEP level, where it is visible
// and reviewable, instead of burying a `|| true` in a shell line.
func (c *Ctx) Run(cmd Cmd) error {
	full := cmd.Name + " " + strings.Join(cmd.Args, " ")
	if c.Log != nil {
		c.Log.writeLine("$ " + full)
	}

	ex := exec.CommandContext(c.Context, cmd.Name, cmd.Args...)
	ex.Dir = cmd.Dir
	if ex.Dir == "" {
		ex.Dir = c.Root
	}
	if len(cmd.Env) > 0 {
		ex.Env = append(os.Environ(), cmd.Env...)
	}

	// Separate pipes rather than a shared writer: both streams are captured, and
	// interleaving them in the log is fine, but sharing one io.Writer between two
	// goroutines without a lock is not.
	stdout, err := ex.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := ex.StderrPipe()
	if err != nil {
		return err
	}
	if err := ex.Start(); err != nil {
		return &ExecError{Cmd: full, Dir: ex.Dir, ExitCode: -1, Err: err}
	}

	var wg sync.WaitGroup
	pump := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if c.Log != nil {
				c.Log.writeLine(line)
			}
			if cmd.Echo {
				fmt.Fprintf(os.Stderr, "  \033[2m%s\033[0m\n", line)
			}
		}
	}
	wg.Add(2)
	go pump(stdout)
	go pump(stderr)
	wg.Wait()

	if err := ex.Wait(); err != nil {
		code := -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return &ExecError{Cmd: full, Dir: ex.Dir, ExitCode: code, Tail: c.Log.Tail(25), Err: err}
	}
	return nil
}

// Output runs a command and returns its stdout, for the small
// "ask a tool a question" cases (an identity, a JSON report).
func (c *Ctx) Output(cmd Cmd) (string, error) {
	full := cmd.Name + " " + strings.Join(cmd.Args, " ")
	ex := exec.CommandContext(c.Context, cmd.Name, cmd.Args...)
	ex.Dir = cmd.Dir
	if ex.Dir == "" {
		ex.Dir = c.Root
	}
	if len(cmd.Env) > 0 {
		ex.Env = append(os.Environ(), cmd.Env...)
	}
	var errBuf strings.Builder
	ex.Stderr = &errBuf
	out, err := ex.Output()
	if err != nil {
		code := -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return "", &ExecError{Cmd: full, Dir: ex.Dir, ExitCode: code, Tail: errBuf.String(), Err: err}
	}
	return strings.TrimSpace(string(out)), nil
}

// RetryClass tells Retry how to behave. Retrying is only ever correct for
// transient conditions; a deterministic parse failure or a full disk must not be
// hammered.
type RetryClass int

const (
	// RetryNetwork: bounded exponential backoff. 3gpp.org rate-limits and times
	// out; those are worth retrying.
	RetryNetwork RetryClass = iota
	// RetryNever: deterministic failures. Diagnose, do not loop.
	RetryNever
)

// Retry runs fn according to a class. Attempts are bounded and logged; there is
// no unbounded loop anywhere in this pipeline.
func (c *Ctx) Retry(class RetryClass, what string, fn func() error) error {
	if class == RetryNever {
		return fn()
	}
	const maxAttempts = 5
	delay := 2 * time.Second
	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		last = fn()
		if last == nil {
			return nil
		}
		if attempt == maxAttempts {
			break
		}
		if c.Log != nil {
			c.Log.Printf("%s failed (attempt %d/%d), retrying in %s: %v", what, attempt, maxAttempts, delay, firstLine(last.Error()))
		}
		select {
		case <-time.After(delay):
		case <-c.Context.Done():
			return c.Context.Err()
		}
		delay *= 2
	}
	return fmt.Errorf("%s failed after %d attempts: %w", what, maxAttempts, last)
}
