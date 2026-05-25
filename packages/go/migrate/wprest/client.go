package wprest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config bundles the inputs for NewClient.
type Config struct {
	// BaseURL is the WordPress site root — the URL the operator
	// browses to, e.g. https://wp-host.example. The client appends
	// /wp-json/wp/v2/ as needed. A trailing slash is tolerated.
	BaseURL string

	// User is the WP login (display name or email) issuing the
	// Application Password. Empty disables auth.
	User string

	// AppPassword is the WP Application Password string as it
	// appears in the admin UI (typically a space-separated 24-char
	// hex stream). Empty disables auth. Application Passwords are
	// passed in HTTP Basic Auth alongside User per WP convention.
	AppPassword string

	// HTTPClient is the underlying transport. Optional; nil uses
	// a default with a 30s timeout. Test callers inject a stub
	// transport here to record/replay fixtures.
	HTTPClient *http.Client

	// PerPage is the page size. The WP REST API caps this at 100
	// for almost every endpoint; values larger than 100 are clamped.
	// Default: 100.
	PerPage int

	// UserAgent overrides the default User-Agent string. Optional.
	UserAgent string
}

// Client fetches records from a WP REST API.
//
// The zero value is unusable; construct via NewClient. Methods are
// safe for concurrent use after construction.
type Client struct {
	baseURL    string
	authHeader string
	http       *http.Client
	perPage    int
	userAgent  string
}

// NewClient validates the Config and returns a Client.
//
// Returns an error if BaseURL is empty or unparseable. Other fields
// have defaults applied.
func NewClient(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("wprest: BaseURL is required")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("wprest: invalid BaseURL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("wprest: BaseURL must include scheme and host")
	}
	base := strings.TrimRight(cfg.BaseURL, "/") + "/wp-json/wp/v2/"

	httpc := cfg.HTTPClient
	if httpc == nil {
		httpc = &http.Client{Timeout: 30 * time.Second}
	}

	perPage := cfg.PerPage
	if perPage <= 0 {
		perPage = 100
	}
	if perPage > 100 {
		perPage = 100
	}

	ua := cfg.UserAgent
	if ua == "" {
		ua = "GoNext-Migrate/1.0"
	}

	c := &Client{
		baseURL:   base,
		http:      httpc,
		perPage:   perPage,
		userAgent: ua,
	}
	if cfg.User != "" && cfg.AppPassword != "" {
		// WP accepts the Application Password with embedded spaces
		// (the form shown in the admin UI). The HTTP Basic spec
		// doesn't care; we still strip them to match what curl
		// users routinely do.
		token := cfg.User + ":" + strings.ReplaceAll(cfg.AppPassword, " ", "")
		c.authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(token))
	}
	return c, nil
}

// fetchPage performs one HTTP GET for the given endpoint and page.
// Returns the response body, the total-pages count from the response
// header, and an error.
//
// The total-pages value is parsed from X-WP-TotalPages; the WP REST
// API guarantees this header on paginated collection endpoints. Its
// absence is non-fatal — we fall back to "1 page" so the caller's
// loop terminates after the first response.
func (c *Client) fetchPage(ctx context.Context, endpoint string, page int) ([]byte, int, error) {
	u, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return nil, 0, fmt.Errorf("wprest: build url: %w", err)
	}
	q := u.Query()
	q.Set("per_page", strconv.Itoa(c.perPage))
	q.Set("page", strconv.Itoa(page))
	// The "context=edit" query parameter asks WP to include private
	// fields (raw content + draft posts) that the default "view"
	// context omits. Migration callers always want everything; the
	// server enforces the operator's capability set, so a non-
	// privileged token simply doesn't get the extra fields.
	q.Set("context", "edit")
	// status=any pulls drafts, pending, private, future, and trash
	// alongside published rows. Without it WP returns only "publish".
	if endpoint == "posts" || endpoint == "pages" {
		q.Set("status", "any")
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("wprest: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("wprest: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("wprest: read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		// Try to decode the WP error JSON so the message surfaces.
		var wpErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &wpErr)
		msg := wpErr.Message
		if msg == "" {
			msg = strings.TrimSpace(string(body))
			if len(msg) > 200 {
				msg = msg[:200] + "…"
			}
		}
		return nil, 0, fmt.Errorf("wprest: %s page %d: HTTP %d: %s",
			endpoint, page, resp.StatusCode, msg)
	}

	totalPages := 1
	if h := resp.Header.Get("X-WP-TotalPages"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 {
			totalPages = n
		}
	}
	return body, totalPages, nil
}

// iteratePages calls fn for each item across all pages of endpoint.
// fn is invoked once per item, in source order. The first error
// returned by fn (or a transport error) stops iteration.
//
// The function is the canonical paging primitive every public Fetch*
// method delegates to.
func iteratePages[T any](
	ctx context.Context,
	c *Client,
	endpoint string,
	fn func(T) error,
) error {
	for page := 1; ; page++ {
		body, totalPages, err := c.fetchPage(ctx, endpoint, page)
		if err != nil {
			return err
		}
		var items []T
		if err := json.Unmarshal(body, &items); err != nil {
			return fmt.Errorf("wprest: decode %s page %d: %w", endpoint, page, err)
		}
		for _, it := range items {
			if err := fn(it); err != nil {
				return err
			}
		}
		if page >= totalPages {
			return nil
		}
	}
}
