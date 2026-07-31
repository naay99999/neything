package apiretry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const defaultMaxAttempts = 3

// networkErrRetryWait is the fixed delay used between retries of a
// connection-level failure (as opposed to an HTTP 429/503, which honors
// Retry-After / a per-attempt wait via retryAfter). There's no response to
// read a hint from for a network error, so this just mirrors the same
// short (~1s) default retryAfter falls back to when a 429/503 response
// doesn't carry a Retry-After header.
const networkErrRetryWait = time.Second

// Do performs req, retrying up to maxAttempts times on:
//   - HTTP 429/503 responses, waiting per Retry-After (header, or a
//     provider's response-body fallback — see retryAfter) between attempts;
//   - transient network errors below the HTTP layer (connection reset/
//     refused, broken pipe, timeouts, EOF/unexpected-EOF on a dropped
//     connection) — these previously caused Do to give up on the first
//     failure even though a brief network blip is exactly the kind of thing
//     a retry is for. All requests this package sends are POSTs to
//     embedding APIs with an idempotent effect (compute an embedding), so
//     retrying them is always safe.
//
// ctx cancellation is always respected: Do returns ctx.Err() immediately
// rather than starting or waiting out another attempt.
func Do(ctx context.Context, client *http.Client, req *http.Request, maxAttempts int) (*http.Response, error) {
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	if client == nil {
		client = http.DefaultClient
	}

	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if body != nil {
			req.Body = io.NopCloser(newBytesReader(body))
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !isRetryableNetErr(err) {
				return nil, err
			}
			lastErr = err
			if attempt == maxAttempts-1 {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(networkErrRetryWait):
			}
			continue
		}

		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
			return resp, nil
		}

		respBody, _ := io.ReadAll(resp.Body)
		wait := retryAfter(resp, respBody)
		resp.Body.Close()
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)

		if attempt == maxAttempts-1 {
			break
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

// retryAfter picks how long to wait before the next attempt after a 429/503:
// the response's Retry-After header if present, else a best-effort fallback
// parse of a provider-specific retry delay reported in the response body
// (e.g. Gemini's 429 body: {"error":{"details":[{"retryDelay":"37s"}]}}),
// else a flat 1s default.
func retryAfter(resp *http.Response, body []byte) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	if d := parseRetryDelayFromBody(body); d > 0 {
		return d
	}
	return time.Second
}

// parseRetryDelayFromBody extracts a `"retryDelay":"<duration>"` field from
// a JSON error body via a plain substring scan (avoids a full JSON decode
// for one optional field, and stays resilient to providers that don't
// otherwise share a common error-body shape). Returns 0 if not present or
// unparseable.
func parseRetryDelayFromBody(body []byte) time.Duration {
	s := string(body)
	idx := strings.Index(s, `"retryDelay"`)
	if idx < 0 {
		return 0
	}
	rest := s[idx+len(`"retryDelay"`):]
	rest = strings.TrimLeft(rest, `: "`)
	end := strings.IndexAny(rest, `"`)
	if end < 0 {
		return 0
	}
	d, err := time.ParseDuration(rest[:end])
	if err != nil {
		return 0
	}
	return d
}

// isRetryableNetErr reports whether err (as returned by http.Client.Do,
// typically wrapped in *url.Error) represents a transient, retry-worthy
// network failure rather than something retrying can't fix (a malformed
// request, an unsupported URL scheme, DNS failure, etc.).
func isRetryableNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

// bytesReader avoids importing bytes in callers that only need replay.
type bytesReader struct {
	data []byte
	off  int
}

func newBytesReader(data []byte) *bytesReader {
	return &bytesReader{data: data}
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
