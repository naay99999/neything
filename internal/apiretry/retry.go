package apiretry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const defaultMaxAttempts = 3

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
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
			return resp, nil
		}

		wait := retryAfter(resp)
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

func retryAfter(resp *http.Response) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return time.Second
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
