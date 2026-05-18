package plugin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// runDev implements `gonext plugin dev [flags] <project-dir>`.
//
// The dev loop is split into four phases:
//
//  1. Detect — language detection (or honour --lang).
//  2. Build — invoke the language-specific toolchain to produce a WASM
//     module at <project>/build/plugin.wasm.
//  3. Upload — POST the WASM + manifest.json multipart to the host's
//     dev-install endpoint.
//  4. Watch (optional) — fsnotify the project dir, debounce events,
//     and re-run build + upload on change. Cancellation flows via the
//     supplied [context.Context] from a SIGINT/SIGTERM handler.
//
// Each phase is wired to an injectable seam so tests can drive the
// orchestrator without touching the filesystem, fork/exec, or the
// network. The defaults wire to the real implementations.
func runDev(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gonext plugin dev", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, devUsage)
	}

	host := fs.String("host", "http://localhost:8080", "URL of the running gonext dev host")
	watch := fs.Bool("watch", true, "watch the project directory and hot-reload on change")
	buildOnly := fs.Bool("build-only", false, "build the WASM artifact and exit; skip upload and watch")
	lang := fs.String("lang", "auto", "build toolchain: auto, go, tinygo, or rust")
	logs := fs.Bool("logs", false, "tail gn_log output from the running plugin (WebSocket)")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "gonext plugin dev: missing project directory")
		fmt.Fprintln(stderr, devUsage)
		return ExitUsage
	}
	if len(rest) > 1 {
		fmt.Fprintf(stderr, "gonext plugin dev: unexpected extra arguments: %v\n", rest[1:])
		fmt.Fprintln(stderr, devUsage)
		return ExitUsage
	}

	projectDir, err := filepath.Abs(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "gonext plugin dev: resolving project dir: %s\n", err)
		return ExitFail
	}
	if st, err := os.Stat(projectDir); err != nil || !st.IsDir() {
		fmt.Fprintf(stderr, "gonext plugin dev: %q is not a directory\n", projectDir)
		return ExitFail
	}

	opts := devOptions{
		ProjectDir: projectDir,
		Host:       *host,
		Watch:      *watch,
		BuildOnly:  *buildOnly,
		Lang:       *lang,
		Logs:       *logs,
		Runner:     execRunner{},
		Uploader:   httpUploader{Client: &http.Client{Timeout: 30 * time.Second}},
		Watcher:    fsnotifyWatcher,
		Tailer:     wsLogTailer{},
		Now:        time.Now,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := runDevLoop(ctx, opts, stdout, stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stdout, "\ngonext plugin dev: stopped")
			return ExitOK
		}
		fmt.Fprintf(stderr, "gonext plugin dev: %s\n", err)
		return ExitFail
	}
	return ExitOK
}

// devOptions captures the configuration and the injectable seams the
// orchestrator uses. Tests construct one with stub Runner, Uploader,
// and Watcher implementations.
type devOptions struct {
	ProjectDir string
	Host       string
	Watch      bool
	BuildOnly  bool
	Lang       string
	Logs       bool

	// Runner executes build commands. Production uses [execRunner].
	Runner CommandRunner
	// Uploader pushes built artifacts to the host. Production uses
	// [httpUploader].
	Uploader Uploader
	// Watcher constructs a [FileWatcher]. Production uses
	// [fsnotifyWatcher].
	Watcher func(dir string) (FileWatcher, error)
	// Tailer streams gn_log events from the dev host. Production uses
	// [wsLogTailer]. Only consulted when Logs is true.
	Tailer LogTailer
	// Now returns the current time. Tests substitute to make TTY
	// timestamps deterministic.
	Now func() time.Time
}

// runDevLoop performs one initial build/upload pass, then — if Watch is
// on and BuildOnly is off — enters the watch loop until ctx is
// cancelled. It is the function-under-test for the orchestrator: every
// I/O dependency is funnelled through opts.
func runDevLoop(ctx context.Context, opts devOptions, stdout, stderr io.Writer) error {
	lang, err := resolveLanguage(opts.ProjectDir, opts.Lang)
	if err != nil {
		return err
	}
	tprintf(stdout, opts.Now, "detected language: %s\n", lang)

	// Track the previous manifest's capability list so we can pretty-print
	// the diff on subsequent builds.
	var prevCaps []string

	build := func() error {
		tprintf(stdout, opts.Now, "build: %s\n", lang)
		if err := buildArtifact(ctx, opts.Runner, opts.ProjectDir, lang, stdout, stderr); err != nil {
			return fmt.Errorf("build: %w", err)
		}
		if opts.BuildOnly {
			tprintf(stdout, opts.Now, "build-only: artifact at %s\n",
				filepath.Join(opts.ProjectDir, "build", "plugin.wasm"))
			return nil
		}

		caps, err := readManifestCapabilities(opts.ProjectDir)
		if err == nil {
			writeCapDiff(stdout, prevCaps, caps)
			prevCaps = caps
		}

		tprintf(stdout, opts.Now, "upload: %s\n", opts.Host)
		if err := opts.Uploader.Upload(ctx, opts.Host, opts.ProjectDir); err != nil {
			return fmt.Errorf("upload: %w", err)
		}
		tprintf(stdout, opts.Now, "uploaded successfully\n")
		return nil
	}

	if err := build(); err != nil {
		// First-pass failures bubble out — the operator hasn't even
		// asked us to watch yet, surface the real error.
		return err
	}

	// Start the log tailer (if requested) once we've successfully
	// uploaded a build at least once. The tailer runs on a child ctx
	// so cancelling the dev loop (Ctrl-C, watcher exit) takes it down
	// alongside everything else.
	//
	// We attach it AFTER the first build/upload so the operator's
	// initial console doesn't fill with errors from a missing-plugin
	// 404 in the WebSocket handshake.
	tailDone := startLogTail(ctx, opts, stdout, stderr)
	defer func() {
		if tailDone != nil {
			<-tailDone
		}
	}()

	if opts.BuildOnly || !opts.Watch {
		return nil
	}

	w, err := opts.Watcher(opts.ProjectDir)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	defer w.Close()

	tprintf(stdout, opts.Now, "watching %s (Ctrl-C to stop)\n", opts.ProjectDir)

	events := debounce(ctx, w.Events(), 200*time.Millisecond, opts.Now)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-w.Errors():
			if !ok {
				return nil
			}
			// Watcher errors are non-fatal — print and keep going.
			fmt.Fprintf(stderr, "watch error: %s\n", err)
		case _, ok := <-events:
			if !ok {
				return nil
			}
			tprintf(stdout, opts.Now, "change detected — rebuilding\n")
			if err := build(); err != nil {
				// In watch mode we keep going on build/upload errors:
				// the operator's next save is probably the fix.
				fmt.Fprintf(stderr, "%s: %s\n", redCross, err)
				continue
			}
		}
	}
}

// tprintf prints a timestamped line. The timestamp uses the supplied
// clock so tests are deterministic.
func tprintf(w io.Writer, now func() time.Time, format string, a ...any) {
	ts := now().Format("15:04:05")
	fmt.Fprintf(w, "[%s] %s", ts, fmt.Sprintf(format, a...))
}

// startLogTail spawns the tailer goroutine when --logs is set and a
// plugin name is resolvable from the manifest. Returns a done channel
// that closes when the tailer goroutine exits, or nil if no tailer
// was started (e.g. flag off, manifest missing, no Tailer wired).
//
// Errors from the tailer are surfaced once to stderr and the
// goroutine exits — we don't retry. The watch loop continues, so the
// operator still gets builds and uploads; they just won't see streamed
// logs until they restart the dev session. This avoids a noisy
// reconnect loop when the dev host is briefly unreachable (e.g.
// during a host-side restart).
func startLogTail(ctx context.Context, opts devOptions, stdout, stderr io.Writer) <-chan struct{} {
	if !opts.Logs || opts.Tailer == nil {
		return nil
	}
	name, err := readManifestName(opts.ProjectDir)
	if err != nil {
		fmt.Fprintf(stderr, "logs: cannot resolve plugin name: %s\n", err)
		return nil
	}
	tprintf(stdout, opts.Now, "tailing logs for %s (Ctrl-C to stop)\n", name)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := opts.Tailer.Tail(ctx, opts.Host, name, stdout); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(stderr, "logs: %s\n", err)
			}
		}
	}()
	return done
}

const redCross = "FAIL"

const devUsage = `gonext plugin dev — author dev loop (auto-detect, build, upload, watch)

Usage:
  gonext plugin dev [flags] <project-dir>

Arguments:
  <project-dir>   Directory containing the plugin source (with a
                  manifest.json at its root, and either go.mod or
                  Cargo.toml to identify the toolchain).

Flags:
  --host         URL of the running gonext dev host. Default
                 http://localhost:8080. The dev install endpoint is
                 POST ${host}/_/plugins/dev/install.

  --watch        Watch the project tree and hot-reload on change.
                 Default: true. Disable with --watch=false.

  --build-only   Build the WASM artifact and exit; skip upload and
                 watch. Useful in CI or for offline checks.

  --lang         Build toolchain. Default: auto. Values:
                   auto    detect from project files (go.mod →
                           tinygo, Cargo.toml → rust)
                   go      synonym for tinygo
                   tinygo  invoke tinygo build -o build/plugin.wasm
                           -target=wasi .
                   rust    invoke cargo build --target wasm32-wasi
                           --release

  --logs         Tail gn_log output from the running plugin in real
                 time. Connects to ws://<host>/_/plugins/dev/logs/
                 <name> after the initial upload and prints one
                 color-coded line per event. Disconnect-and-retry is
                 not performed — once the stream drops you'll need to
                 restart the dev session.

Exit codes:
  0   build (and upload, if not --build-only) succeeded; or watch
      session terminated cleanly
  1   detection, build, or upload failed
  2   usage error (bad flags or missing argument)

The watch loop debounces events at 200ms so a single save that
triggers multiple inotify events only kicks off one rebuild. Build or
upload errors during watch are printed but do not exit — the next
save will retry. Ctrl-C (SIGINT) or SIGTERM stops the watcher and
exits 0.`
