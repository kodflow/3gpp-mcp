package goal

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// shellCommand is one simple command of a bash script, as much of one as a test
// needs: the line it starts on, the NAME=value assignments in front of it, and
// its words with the quoting removed.
type shellCommand struct {
	Line  int      // 1-based line the command starts on
	Env   []string // leading NAME=value assignments, in order
	Words []string // the command word first, then its arguments
}

// readShellCommands splits a bash script into its simple commands the way bash
// reads them, as far as a test asking "which cargo and go commands does this
// script run, with which flags" needs.
//
// WHY A LEXER AND NOT A LINE SCAN. The reader this replaces (6cafdc7, 2026-09-11)
// joined continuation lines, then took the first bare `cargo` word of each line
// and every word after it as that command's arguments. Review found four shapes it
// got wrong, and the first one is this script's own idiom (VERSION="$(git …)",
// CDYLIB_W="$(cd …)"):
//
//	X="$(cargo metadata …)"      `X="$(cargo` is one word: never seen
//	cargo +stable build …        the override stood where the subcommand goes:
//	                             never seen
//	cargo build …; cargo test …  the second was never read, and would have been
//	                             judged by the first one's flags anyway
//	cargo build … # --locked     the comment passed the lock check
//
// So this reads what bash reads. Continuations are joined, inside double quotes
// too. A comment is a `#` that starts a word outside quotes, and it runs to the
// end of its line whatever that line ends with. Quotes are removed; heredoc bodies
// are skipped. A command ends at a newline, `;`, `&`, `&&`, `|`, `||`, a
// parenthesis, or the close of the `$( )` or backtick substitution it runs in. A
// substitution is a command of its own, and the command around it sees the
// placeholder word `$(…)` where it stood. `2>&1`, `>&2` and `&>` are redirections,
// not a `&` ending the command.
//
// It expands nothing. A cargo reached through a variable ("$CARGO build") stays
// invisible, and the callers' count checks fail if every invocation vanishes.
// `${ }`, `$(( ))` and `(( ))` are kept as literal pieces of a word.
func readShellCommands(src string) []shellCommand {
	l := &shellLexer{src: strings.ReplaceAll(src, "\r\n", "\n"), line: 1}
	l.frames = []*shellFrame{{}}
	l.run()
	return l.out
}

// shellFrame is one level of reading: the script itself, or a substitution in it.
type shellFrame struct {
	cmd    shellCommand
	word   []byte
	inWord bool // a word has begun; "" is a word too
	quoted bool // inside double quotes at this level
	closer byte // ')' for $( ), '`' for a backtick, 0 for the script
	parens int  // plain ( opened at this level, whose ) must not close a $( )
}

type shellLexer struct {
	src      string
	i        int
	line     int
	frames   []*shellFrame
	out      []shellCommand
	heredocs []shellHeredoc // bodies to skip once the current line ends
}

type shellHeredoc struct {
	delim     string
	stripTabs bool // <<- strips leading tabs from the body, delimiter line included
}

func (l *shellLexer) top() *shellFrame { return l.frames[len(l.frames)-1] }

// peek is the byte k ahead of the cursor, 0 past the end.
func (l *shellLexer) peek(k int) byte {
	if l.i+k < len(l.src) {
		return l.src[l.i+k]
	}
	return 0
}

// begin opens a word if none is open, and records the line of the command it
// starts when it is the command's first.
func (l *shellLexer) begin() {
	f := l.top()
	if f.inWord {
		return
	}
	f.inWord = true
	if len(f.cmd.Env) == 0 && len(f.cmd.Words) == 0 {
		f.cmd.Line = l.line
	}
}

func (l *shellLexer) add(s string) {
	l.begin()
	f := l.top()
	f.word = append(f.word, s...)
}

func (l *shellLexer) endWord() {
	f := l.top()
	if !f.inWord {
		return
	}
	w := string(f.word)
	f.word, f.inWord = f.word[:0], false
	if len(f.cmd.Words) == 0 && isShellAssignment(w) {
		f.cmd.Env = append(f.cmd.Env, w)
		return
	}
	f.cmd.Words = append(f.cmd.Words, w)
}

func (l *shellLexer) endCommand() {
	l.endWord()
	f := l.top()
	if len(f.cmd.Words) > 0 {
		l.out = append(l.out, f.cmd)
	}
	f.cmd = shellCommand{}
}

// closeSubstitution ends the $( ) or backtick level being read: its command is
// complete, and the word it stood in goes on one level up.
func (l *shellLexer) closeSubstitution() {
	l.endCommand()
	l.frames = l.frames[:len(l.frames)-1]
	l.add("$(…)")
}

func (l *shellLexer) run() {
	for l.i < len(l.src) {
		f := l.top()
		c := l.src[l.i]
		switch {
		case c == '\\':
			l.backslash()
		case c == '\'' && !f.quoted:
			l.singleQuoted()
		case c == '"':
			l.begin()
			f.quoted = !f.quoted
			l.i++
		case c == '$' && l.peek(1) == '(' && l.peek(2) == '(':
			l.balanced('(', ')')
		case c == '$' && l.peek(1) == '(':
			l.begin()
			l.frames = append(l.frames, &shellFrame{closer: ')'})
			l.i += 2
		case c == '$' && l.peek(1) == '{':
			l.balanced('{', '}')
		case c == '`' && f.closer == '`':
			l.closeSubstitution()
			l.i++
		case c == '`':
			l.begin()
			l.frames = append(l.frames, &shellFrame{closer: '`'})
			l.i++
		case f.quoted:
			if c == '\n' {
				l.line++
			}
			l.add(string(c))
			l.i++
		case c == '#' && !f.inWord:
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.i++
			}
		case c == '\n':
			l.endCommand()
			l.line++
			l.i++
			l.skipHeredocs()
		case c == ' ' || c == '\t':
			l.endWord()
			l.i++
		case c == ')' && f.closer == ')' && f.parens == 0:
			l.closeSubstitution()
			l.i++
		case c == '(' && l.peek(1) == '(' && !f.inWord:
			l.balanced('(', ')') // (( arithmetic ))
		case c == '(' || c == ')':
			if c == '(' {
				f.parens++
			} else if f.parens > 0 {
				f.parens--
			}
			l.endCommand()
			l.i++
		case c == '<' && l.peek(1) == '<' && l.peek(2) == '<':
			l.add("<<<") // a here-string: the word after it is read as usual
			l.i += 3
		case c == '<' && l.peek(1) == '<':
			l.heredoc()
		case c == '&' && (l.peek(1) == '>' || l.wordEndsInRedirect()):
			l.add("&")
			l.i++
		case c == ';' || c == '&' || c == '|':
			l.endCommand()
			l.i++
		default:
			l.add(string(c))
			l.i++
		}
	}
	for len(l.frames) > 1 {
		l.closeSubstitution()
	}
	l.endCommand()
}

func (l *shellLexer) backslash() {
	n := l.peek(1)
	switch {
	case n == '\n':
		// A continuation: bash removes both characters, inside double quotes too,
		// so the command, and the word, go on at the next line.
		l.i += 2
		l.line++
	case n == 0:
		l.add(`\`)
		l.i++
	case l.top().quoted && !strings.ContainsRune("\"\\$`", rune(n)):
		// Inside double quotes a backslash escapes only these four; before anything
		// else it is itself.
		l.add(`\`)
		l.i++
	default:
		l.add(string(n))
		l.i += 2
	}
}

func (l *shellLexer) singleQuoted() {
	l.begin()
	end := strings.IndexByte(l.src[l.i+1:], '\'')
	if end < 0 {
		end = len(l.src) - l.i - 1
	}
	lit := l.src[l.i+1 : l.i+1+end]
	l.add(lit)
	l.line += strings.Count(lit, "\n")
	l.i += end + 2
}

// balanced keeps a ${ }, $(( )) or (( )) as one literal piece of the current word:
// nothing inside one is a command this reader has to see.
func (l *shellLexer) balanced(open, close byte) {
	start, depth := l.i, 0
	for l.i < len(l.src) {
		c := l.src[l.i]
		l.i++
		if c == '\n' {
			l.line++
		}
		if c == open {
			depth++
		}
		if c == close {
			if depth--; depth == 0 {
				break
			}
		}
	}
	l.add(l.src[start:l.i])
}

// heredoc reads a << or <<- operator and its delimiter. The body is skipped when
// the line ends, by skipHeredocs.
func (l *shellLexer) heredoc() {
	l.endWord()
	l.i += 2
	h := shellHeredoc{}
	if l.peek(0) == '-' {
		h.stripTabs = true
		l.i++
	}
	for l.peek(0) == ' ' || l.peek(0) == '\t' {
		l.i++
	}
	var d []byte
	for l.i < len(l.src) && !strings.ContainsRune(" \t\n;&|<>()", rune(l.src[l.i])) {
		// Quoting the delimiter (<<'YAML') only stops expansion in the body, which
		// this reader never performs; the delimiter itself is the unquoted text.
		if c := l.src[l.i]; c != '\'' && c != '"' && c != '\\' {
			d = append(d, c)
		}
		l.i++
	}
	h.delim = string(d)
	l.heredocs = append(l.heredocs, h)
}

// skipHeredocs steps over the bodies of the heredocs opened on the line that just
// ended, in order, each up to and including its delimiter line.
func (l *shellLexer) skipHeredocs() {
	for _, h := range l.heredocs {
		for l.i < len(l.src) {
			ln := l.src[l.i:]
			if j := strings.IndexByte(ln, '\n'); j >= 0 {
				ln = ln[:j]
				l.i += j + 1
				l.line++
			} else {
				l.i = len(l.src)
			}
			if h.stripTabs {
				ln = strings.TrimLeft(ln, "\t")
			}
			if ln == h.delim {
				break
			}
		}
	}
	l.heredocs = nil
}

// wordEndsInRedirect reports whether the word being read ends in > or <, so a &
// after it is a file-descriptor redirection (2>&1, >&2), not a command separator.
func (l *shellLexer) wordEndsInRedirect() bool {
	f := l.top()
	return len(f.word) > 0 && (f.word[len(f.word)-1] == '>' || f.word[len(f.word)-1] == '<')
}

// isShellAssignment reports whether w is NAME=value, which bash reads as an
// assignment when it comes before the command word.
func isShellAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	name = strings.TrimSuffix(name, "+")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		letter := r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
		if !letter && !(i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// TestTheShellReaderReadsWhatBashRuns pins the lexer the script tests stand on.
//
// Every case is a shape a real script here uses, or one the 6cafdc7 reader got
// wrong; the four named over readShellCommands come first. A reader that regressed
// on one of them would not fail the script tests — it would make them check less —
// so the shapes are held here, one by one, against synthetic scripts.
func TestTheShellReaderReadsWhatBashRuns(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string // "line: env | words", one per command, in order
	}{
		{"a substitution is its own command",
			"X=\"$(cargo metadata --manifest-path a/Cargo.toml)\" && echo \"$X\"\n",
			[]string{"1: | cargo metadata --manifest-path a/Cargo.toml", "1: | echo $X"}},
		{"the command around a substitution goes on, holding a placeholder",
			"V=\"$(git describe)\" go build -ldflags \"-X main.V=$V\" ./cmd/x\n",
			[]string{"1: | git describe", "1: V=$(…) | go build -ldflags -X main.V=$V ./cmd/x"}},
		{"a backtick substitution too",
			"X=`cargo tree`\n",
			[]string{"1: | cargo tree"}},
		{"a toolchain override stays an argument",
			"cargo +stable build\n",
			[]string{"1: | cargo +stable build"}},
		{"two commands on one line are two commands",
			"cargo build --locked; cargo test\ncargo check && cargo doc || cargo fix | cat\n",
			[]string{"1: | cargo build --locked", "1: | cargo test",
				"2: | cargo check", "2: | cargo doc", "2: | cargo fix", "2: | cat"}},
		{"a comment is not an argument, and never continues",
			"cargo build # --locked \\\ncargo test\n# cargo run\n",
			[]string{"1: | cargo build", "2: | cargo test"}},
		{"a # inside a word or in quotes is not a comment",
			"echo a#b \"c # d\" ${e#f} $#\n",
			[]string{"1: | echo a#b c # d ${e#f} $#"}},
		{"continuations are joined and the command keeps its first line",
			"A=1 \\\n  B=\"x y\" \\\n  cargo build --release \\\n    --locked\n",
			[]string{"1: A=1 B=x y | cargo build --release --locked"}},
		{"quotes nest through a substitution inside double quotes",
			"D=\"$(cd \"$(dirname \"$C\")\" && (pwd -W 2>/dev/null || pwd))\"\n",
			[]string{"1: | dirname $C", "1: | cd $(…)", "1: | pwd -W 2>/dev/null", "1: | pwd"}},
		{"redirections do not end a command",
			"cargo build >/dev/null 2>&1 --locked &>log >&2\n",
			[]string{"1: | cargo build >/dev/null 2>&1 --locked &>log >&2"}},
		{"a heredoc body is not commands, and a here-string is not a heredoc",
			"cat > f <<'YAML'\ncargo build\nYAML\nread x <<<\"$y\"\ncargo test\n",
			[]string{"1: | cat > f", "4: | read x <<<$y", "5: | cargo test"}},
		{"line numbers survive multi-line quotes and continuations",
			"echo 'a\nb'\nX=1 \\\n cargo build\n",
			[]string{"1: | echo a\nb", "3: X=1 | cargo build"}},
		{"CRLF reads as LF",
			"cargo build \\\r\n  --locked\r\n",
			[]string{"1: | cargo build --locked"}},
		{"arithmetic is a literal",
			"n=$(( 10#${p:0:1} )); (( n > 1 )) && cargo build\n",
			[]string{"1: | (( n > 1 ))", "1: | cargo build"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, c := range readShellCommands(tc.src) {
				env := strings.Join(c.Env, " ")
				if env != "" {
					env += " "
				}
				got = append(got, fmt.Sprintf("%d: %s| %s", c.Line, env, strings.Join(c.Words, " ")))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("read %q as\n  %s\nwant\n  %s", tc.src, strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}
