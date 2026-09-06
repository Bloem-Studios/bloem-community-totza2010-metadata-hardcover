// Package hardcover talks to the Hardcover GraphQL API and maps its responses
// onto metadata.Book.
package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// BaseURL is the Hardcover GraphQL endpoint.
const BaseURL = "https://api.hardcover.app/v1/graphql"

const (
	// maxResponseBytes caps a single GraphQL response body.
	maxResponseBytes = 8 * 1024 * 1024
	// requestTimeout bounds one HTTP round trip. Hardcover's own query
	// timeout is 30s, so this leaves room for it to answer or fail.
	requestTimeout = 30 * time.Second
	// maxRetries is how many times a throttled or temporarily unavailable
	// request is retried before giving up.
	maxRetries = 2
	// defaultPerMinute and defaultBurst match Hardcover's free plan.
	defaultPerMinute = 60
	defaultBurst     = 10
	// defaultSearchLimit is how many search hits are requested per query.
	defaultSearchLimit = 20
)

// ErrNoAPIKey is returned when the plugin is asked to call Hardcover before an
// API key has been configured.
var ErrNoAPIKey = errors.New("hardcover: no API key configured")

// Options configures a Client.
type Options struct {
	// BaseURL overrides the GraphQL endpoint. Empty uses BaseURL.
	BaseURL string
	// APIKey is the Hardcover personal access token. Requests are refused
	// without one.
	APIKey string
	// UserAgent identifies the plugin to Hardcover, which asks integrations
	// to send one.
	UserAgent string
	// HTTPClient overrides the HTTP client. Empty uses a client with
	// requestTimeout.
	HTTPClient *http.Client
	// RequestsPerMinute and Burst override the client-side rate limit.
	RequestsPerMinute int
	Burst             int
	// SearchLimit caps how many search hits are returned.
	SearchLimit int
}

// Client is a rate-limited Hardcover GraphQL client.
type Client struct {
	baseURL     string
	apiKey      string
	userAgent   string
	httpClient  *http.Client
	limiter     *rate.Limiter
	searchLimit int
}

// New builds a Client from options.
func New(options Options) *Client {
	baseURL := strings.TrimSpace(options.BaseURL)
	if baseURL == "" {
		baseURL = BaseURL
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}

	perMinute := options.RequestsPerMinute
	if perMinute <= 0 {
		perMinute = defaultPerMinute
	}
	burst := options.Burst
	if burst <= 0 {
		burst = defaultBurst
	}
	searchLimit := options.SearchLimit
	if searchLimit <= 0 {
		searchLimit = defaultSearchLimit
	}

	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		apiKey:      normalizeAPIKey(options.APIKey),
		userAgent:   strings.TrimSpace(options.UserAgent),
		httpClient:  httpClient,
		limiter:     rate.NewLimiter(rate.Limit(float64(perMinute)/60.0), burst),
		searchLimit: searchLimit,
	}
}

// Configured reports whether the client has an API key.
func (c *Client) Configured() bool {
	return c != nil && c.apiKey != ""
}

// normalizeAPIKey accepts a raw token or one already prefixed with "Bearer ",
// which is how Hardcover's console displays it.
func normalizeAPIKey(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 7 && strings.EqualFold(value[:7], "bearer ") {
		return strings.TrimSpace(value[7:])
	}
	return value
}

// graphqlError is one entry of a GraphQL errors array.
type graphqlError struct {
	Message string `json:"message"`
}

// envelope is the part of a response that reports failure. Hardcover answers
// auth and quota problems with a bare "error" string rather than the GraphQL
// "errors" array, so both are decoded.
type envelope struct {
	Errors           []graphqlError `json:"errors"`
	Error            string         `json:"error"`
	ErrorDescription string         `json:"error_description"`
	Message          string         `json:"message"`
}

func (e envelope) failure() string {
	if len(e.Errors) > 0 && strings.TrimSpace(e.Errors[0].Message) != "" {
		return strings.TrimSpace(e.Errors[0].Message)
	}
	if text := strings.TrimSpace(e.Error); text != "" {
		if description := firstNonEmpty(e.ErrorDescription, e.Message); description != "" {
			return text + ": " + description
		}
		return text
	}
	return ""
}

// execute runs one GraphQL document and decodes its data field into dest.
func (c *Client) execute(ctx context.Context, query string, variables map[string]any, dest any) error {
	if !c.Configured() {
		return ErrNoAPIKey
	}

	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return fmt.Errorf("hardcover: encode request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}

		body, status, header, err := c.post(ctx, payload)
		if err != nil {
			return err
		}

		if delay, retry := retryDelay(status, header); retry && attempt < maxRetries {
			lastErr = fmt.Errorf("hardcover: status %d", status)
			if err := sleepContext(ctx, delay); err != nil {
				return err
			}
			continue
		}

		var failure envelope
		// A body that is not JSON only matters when the status was also
		// bad, which the status check below reports.
		_ = json.Unmarshal(body, &failure)
		if message := failure.failure(); message != "" {
			return fmt.Errorf("hardcover: %s", message)
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			return fmt.Errorf("hardcover: status %d", status)
		}

		var result struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("hardcover: decode response: %w", err)
		}
		if len(result.Data) == 0 {
			return nil
		}
		if err := json.Unmarshal(result.Data, dest); err != nil {
			return fmt.Errorf("hardcover: decode data: %w", err)
		}
		return nil
	}

	if lastErr != nil {
		return lastErr
	}
	return errors.New("hardcover: request failed")
}

// post sends the payload and reads a capped response body.
func (c *Client) post(ctx context.Context, payload []byte) ([]byte, int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("hardcover: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The endpoint carries no secret; the token travels in a header, so
		// reporting the URL alone leaks nothing.
		return nil, 0, nil, fmt.Errorf("hardcover: request to %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("hardcover: read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("hardcover: response exceeds %d bytes", maxResponseBytes)
	}
	return body, resp.StatusCode, resp.Header, nil
}

// retryDelay reports how long to wait before retrying a throttled or
// temporarily unavailable response.
func retryDelay(status int, header http.Header) (time.Duration, bool) {
	if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
		return 0, false
	}
	if delay := parseRetryAfter(header.Get("Retry-After")); delay > 0 {
		return delay, true
	}
	if delay := parseRateLimitReset(header.Get("RateLimit")); delay > 0 {
		return delay, true
	}
	return time.Second, true
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return capDelay(time.Duration(seconds) * time.Second)
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := time.Until(at); delay > 0 {
			return capDelay(delay)
		}
	}
	return 0
}

// parseRateLimitReset reads the seconds-to-reset field out of an IETF
// RateLimit header such as: "Free";r=0;t=42, "daily";r=4231;t=51234
func parseRateLimitReset(value string) time.Duration {
	for _, part := range strings.Split(value, ";") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(part), "t=")
		if !ok {
			continue
		}
		rest, _, _ = strings.Cut(rest, ",")
		if seconds, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil && seconds > 0 {
			return capDelay(time.Duration(seconds) * time.Second)
		}
	}
	return 0
}

// capDelay keeps a retry inside the request budget Silo allows a provider.
func capDelay(delay time.Duration) time.Duration {
	const maxDelay = 5 * time.Second
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
