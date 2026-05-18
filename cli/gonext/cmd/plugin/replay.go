package plugin

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/health"
)

// runReplay implements `gonext plugin replay [--server URL] <plugin> <trap-id>`.
//
// It fetches the trap event from the live admin endpoint
// (/api/v1/plugins/<plugin>/traps/<id>) and re-runs the captured
// invocation against the currently-loaded plugin bytes via the
// dev-mode replay endpoint. The output is a small diff: what the
// stored event recorded vs. what the re-run produced.
//
// This is a dev-mode-only command: production servers do not expose a
// replay endpoint (re-running a possibly-malicious payload against a
// patched plugin is a privileged operation), and the CLI surfaces a
// distinct 403 message in that case so authors don't blame the
// network.
func runReplay(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gonext plugin replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, replayUsage)
	}
	server := fs.String("server", "http://127.0.0.1:8080", "base URL of the GoNext admin server")
	jsonOut := fs.Bool("json", false, "emit the diff as a single JSON object on stdout")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP request timeout for both calls")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintln(stderr, "gonext plugin replay: expected <plugin> <trap-id>")
		fmt.Fprint(stderr, replayUsage)
		return ExitUsage
	}
	pluginSlug, trapID := rest[0], rest[1]

	client := &http.Client{Timeout: *timeout}
	diff, err := executeReplay(client, *server, pluginSlug, trapID)
	if err != nil {
		fmt.Fprintf(stderr, "gonext plugin replay: %v\n", err)
		return ExitFail
	}
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(diff); err != nil {
			fmt.Fprintf(stderr, "gonext plugin replay: encode: %v\n", err)
			return ExitFail
		}
		if diff.Matches {
			return ExitOK
		}
		return ExitFail
	}
	renderDiff(stdout, diff)
	if diff.Matches {
		return ExitOK
	}
	return ExitFail
}

// ReplayDiff is the structured shape `gonext plugin replay` emits
// (and what the JSON mode encodes verbatim). The fields are aligned
// with what a UI cares about: the original trap context, the rerun
// outcome, and a single Matches boolean for scripted use.
type ReplayDiff struct {
	Plugin string `json:"plugin"`
	TrapID uint64 `json:"trap_id"`
	Hook   string `json:"hook"`

	OriginalReason string `json:"original_reason"`
	RerunReason    string `json:"rerun_reason,omitempty"`
	RerunResult    string `json:"rerun_result"`
	Matches        bool   `json:"matches"`
}

// executeReplay is split out so unit tests can drive it against an
// httptest.Server without going through the flag-parsing surface.
func executeReplay(client *http.Client, server, pluginSlug, trapID string) (ReplayDiff, error) {
	trapURL, err := joinHealthURL(server, "/api/v1/plugins/", pluginSlug, "/traps/", trapID)
	if err != nil {
		return ReplayDiff{}, fmt.Errorf("build trap url: %w", err)
	}
	resp, err := client.Get(trapURL)
	if err != nil {
		return ReplayDiff{}, fmt.Errorf("fetch trap: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ReplayDiff{}, fmt.Errorf("trap %s for plugin %q not found (may have aged out of the ring)", trapID, pluginSlug)
	}
	if resp.StatusCode != http.StatusOK {
		return ReplayDiff{}, fmt.Errorf("fetch trap: server returned %s", resp.Status)
	}
	var ev health.TrapEvent
	if err := json.NewDecoder(resp.Body).Decode(&ev); err != nil {
		return ReplayDiff{}, fmt.Errorf("decode trap event: %w", err)
	}

	replayURL, err := joinHealthURL(server, "/api/v1/plugins/", pluginSlug, "/replay")
	if err != nil {
		return ReplayDiff{}, fmt.Errorf("build replay url: %w", err)
	}
	reqBody, _ := json.Marshal(replayRequest{
		Hook:    ev.Hook,
		Payload: ev.Payload,
	})
	rerunResp, err := client.Post(replayURL, "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		return ReplayDiff{}, fmt.Errorf("post replay: %w", err)
	}
	defer rerunResp.Body.Close()
	if rerunResp.StatusCode == http.StatusForbidden {
		return ReplayDiff{}, fmt.Errorf("replay refused: server is not in dev mode (got 403)")
	}
	if rerunResp.StatusCode != http.StatusOK {
		return ReplayDiff{}, fmt.Errorf("post replay: server returned %s", rerunResp.Status)
	}
	var rerun replayResult
	if err := json.NewDecoder(rerunResp.Body).Decode(&rerun); err != nil {
		return ReplayDiff{}, fmt.Errorf("decode replay result: %w", err)
	}

	diff := ReplayDiff{
		Plugin:         pluginSlug,
		TrapID:         ev.ID,
		Hook:           ev.Hook,
		OriginalReason: ev.Reason,
		RerunReason:    rerun.Reason,
		RerunResult:    rerun.Result,
	}
	// "Matches" means the re-run produced the same outcome as the
	// recorded trap — useful for confirming that a patch *did*
	// change behaviour (Matches == false means the bug is gone, or
	// at least mutated). If the rerun reported the same trap
	// reason, we consider it a match.
	if rerun.Result == "trap" && rerun.Reason == ev.Reason {
		diff.Matches = true
	}
	return diff, nil
}

// replayRequest is the body posted to the dev-mode replay endpoint.
// Kept in this file (rather than the health package) because it is
// a CLI/server contract that only the dev-mode wiring honours;
// production servers should not expose the route at all.
type replayRequest struct {
	Hook    string `json:"hook"`
	Payload []byte `json:"payload"`
}

// replayResult is the shape returned by the dev-mode replay
// endpoint. result is one of the health.Result* constants; reason
// is populated for trap-flavoured results.
type replayResult struct {
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

// renderDiff prints a human-friendly summary of the replay diff. The
// CLI defaults to this; --json switches to the structured form.
func renderDiff(w io.Writer, d ReplayDiff) {
	fmt.Fprintf(w, "Plugin     %s\n", d.Plugin)
	fmt.Fprintf(w, "Trap ID    %d\n", d.TrapID)
	fmt.Fprintf(w, "Hook       %s\n", d.Hook)
	fmt.Fprintln(w, "---")
	fmt.Fprintf(w, "Original   %s\n", d.OriginalReason)
	if d.RerunResult == "trap" {
		fmt.Fprintf(w, "Re-run     trap: %s\n", d.RerunReason)
	} else {
		fmt.Fprintf(w, "Re-run     %s\n", d.RerunResult)
	}
	fmt.Fprintln(w, "---")
	if d.Matches {
		fmt.Fprintln(w, "Result     same failure reproduced — bug still present")
	} else {
		fmt.Fprintln(w, "Result     behaviour changed — patch may have fixed it")
	}
}

// joinHealthURL concatenates segments into a valid URL. We avoid path.Join
// on the whole URL because it eats the "://", and we cannot use
// url.URL.JoinPath alone because the segments contain raw path
// elements with possibly-unescaped colons.
func joinHealthURL(base string, segments ...string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	joined := u.Path
	for _, seg := range segments {
		joined = path.Join(joined, seg)
	}
	// path.Join collapses repeated slashes — that's fine for our
	// fixed path layout.
	u.Path = joined
	return u.String(), nil
}

const replayUsage = `gonext plugin replay — re-run a recorded plugin trap against the current bytes

Usage:
  gonext plugin replay [--server URL] [--timeout DURATION] [--json] <plugin> <trap-id>

Flags:
  --server    base URL of the GoNext admin server (default http://127.0.0.1:8080).
  --timeout   per-request HTTP timeout (default 10s).
  --json      emit the diff as a single JSON object on stdout instead of the
              human-readable summary.

The command fetches the trap event from /api/v1/plugins/<plugin>/traps/<trap-id>,
then re-runs the same hook invocation against the currently-loaded plugin bytes
via the dev-mode /api/v1/plugins/<plugin>/replay endpoint. The output describes
the diff between the recorded trap and the re-run outcome.

The replay endpoint is dev-mode-only; production servers respond 403, and the
command surfaces that as a distinct error.

Exit codes:
  0   re-run reproduced the same failure (bug still present)
  1   re-run produced a different outcome (likely fixed), or any error
  2   bad arguments
`
