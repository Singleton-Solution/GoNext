package initcmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"

	"golang.org/x/term"
)

// minPasswordLen is the floor we enforce for the initial admin
// password. RFC 9106 has no opinion on input length, but we don't want
// a 4-character password protecting the only account that can create
// other accounts. 12 chars is the NIST SP 800-63B minimum for
// "memorized secrets" that an operator manages personally; weaker
// passwords are rejected at the CLI rather than letting them quietly
// land in user_passwords.password_hash.
const minPasswordLen = 12

// ErrPasswordTooShort is returned when the supplied password is below
// minPasswordLen. The CLI surfaces this with exit code 2 (usage),
// because the operator is the one supplying the field — the right fix
// is to pick a stronger password, not to retry harder.
var ErrPasswordTooShort = errors.New("password must be at least 12 characters")

// ErrInvalidEmail is returned when the supplied email cannot be
// parsed by net/mail. We don't validate against DNS — that would make
// `gonext init` flake on offline laptops — but we do require the
// address to be syntactically well-formed.
var ErrInvalidEmail = errors.New("email is not a valid address")

// validateEmail returns nil iff s parses as a single RFC 5322
// address. Pure function — safe to call from tests.
//
// We trim whitespace before parsing because operators routinely
// paste with leading/trailing spaces from password managers. The
// trimmed value is what the rest of the pipeline writes to the DB.
func validateEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidEmail, err)
	}
	// mail.ParseAddress accepts "Name <addr>" forms. We only want the
	// bare address — the rest of the pipeline writes users.email which
	// is a CITEXT column, not an RFC 5322 mailbox.
	return addr.Address, nil
}

// validatePassword returns nil iff p meets the minimum-length floor.
// No entropy check — per design (see issue body): "min 12 chars, no
// entropy check". That keeps the failure modes predictable and the
// validation cost zero.
func validatePassword(p string) error {
	if len(p) < minPasswordLen {
		return ErrPasswordTooShort
	}
	return nil
}

// prompter is the small abstraction layer over stdin so tests can
// inject a known fixture without faking a terminal. Production code
// constructs the OS-backed implementation via newOSPrompter; tests
// build a stringPrompter from a script.
type prompter interface {
	// readLine prompts label on out and returns the response, trimmed
	// of trailing newline. Empty responses are returned verbatim — the
	// caller decides whether to loop or accept the default.
	readLine(label string) (string, error)

	// readPassword prompts label on out and returns the response with
	// echo suppressed when the implementation supports it. Empty
	// responses are returned verbatim.
	readPassword(label string) (string, error)
}

// osPrompter is the production prompter. It writes prompts to out
// and reads responses from in. The reader is wrapped in a bufio.Reader
// because os.Stdin is unbuffered and we need ReadString('\n').
//
// fd is the file descriptor used to switch the terminal into
// no-echo mode. When fd is not a terminal (piped input in CI),
// readPassword silently falls back to readLine — the test harness
// pipes scripted answers, and demanding a real TTY there would just
// make the tests harder to run.
type osPrompter struct {
	in  *bufio.Reader
	out io.Writer
	fd  int
}

// newOSPrompter wires a prompter to the given streams. fd should be
// the underlying file descriptor of in (e.g. int(os.Stdin.Fd())) so
// password hiding can switch the terminal into no-echo mode.
func newOSPrompter(in io.Reader, out io.Writer, fd int) *osPrompter {
	return &osPrompter{
		in:  bufio.NewReader(in),
		out: out,
		fd:  fd,
	}
}

// readLine implements prompter. The trailing '\n' is stripped; a
// bare '\r' (Windows line ending) is also stripped because Windows
// stdin in cmd.exe ships CRLF.
func (p *osPrompter) readLine(label string) (string, error) {
	if _, err := fmt.Fprint(p.out, label); err != nil {
		return "", err
	}
	line, err := p.in.ReadString('\n')
	// ReadString returns io.EOF along with the final partial line if
	// the stream ends without a newline. We treat that as a complete
	// answer — the operator's last line is still meaningful.
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	return line, nil
}

// readPassword implements prompter with echo suppression when fd is
// a TTY. The newline after the captured password is emitted manually
// because no-echo also suppressed the user's own Return keystroke.
func (p *osPrompter) readPassword(label string) (string, error) {
	if _, err := fmt.Fprint(p.out, label); err != nil {
		return "", err
	}
	if !term.IsTerminal(p.fd) {
		// No TTY — almost certainly a piped stream in CI or a test.
		// Fall back to a buffered read so the operator's scripted
		// input still works. We deliberately do NOT log a warning
		// here: stdout is the human's surface, not a place to
		// announce "you're being piped".
		line, err := p.in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	raw, err := term.ReadPassword(p.fd)
	// Emit the newline the user's Return didn't echo, so subsequent
	// prompts don't appear on the same visual row.
	if _, fErr := fmt.Fprintln(p.out); fErr != nil && err == nil {
		err = fErr
	}
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// stringPrompter is a deterministic prompter for tests. It serves
// scripted responses in order; once exhausted, subsequent reads return
// io.EOF so the test fails loudly rather than blocking on stdin.
//
// Field choice: lines and passwords share a single slice on purpose.
// Real terminals don't distinguish "line answer" from "password
// answer" — the type of the prompt is up to the caller, not the
// stream. Test scripts are written in the order the prompts appear,
// which matches what the user types.
type stringPrompter struct {
	answers []string
	cursor  int
	out     io.Writer
}

func newStringPrompter(out io.Writer, answers []string) *stringPrompter {
	return &stringPrompter{answers: answers, out: out}
}

func (p *stringPrompter) next(label string) (string, error) {
	if p.out != nil {
		_, _ = fmt.Fprint(p.out, label)
	}
	if p.cursor >= len(p.answers) {
		return "", io.EOF
	}
	v := p.answers[p.cursor]
	p.cursor++
	if p.out != nil {
		// Mirror the answer back so the captured stdout reads naturally
		// in tests (label + answer). Password prompts would normally
		// have no echo — for test fidelity we echo with a newline.
		_, _ = fmt.Fprintln(p.out, v)
	}
	return v, nil
}

func (p *stringPrompter) readLine(label string) (string, error)     { return p.next(label) }
func (p *stringPrompter) readPassword(label string) (string, error) { return p.next(label) }
