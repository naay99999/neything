package apiretry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// flakyThenOKTransport fails the first failN RoundTrips with a retryable
// network error (io.EOF, standing in for "connection dropped mid-response"),
// then delegates to a real transport.
type flakyThenOKTransport struct {
	failN     int
	calls     int
	transport http.RoundTripper
}

func (t *flakyThenOKTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	if t.calls <= t.failN {
		return nil, io.EOF
	}
	return t.transport.RoundTrip(req)
}

func TestDoRetriesConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	transport := &flakyThenOKTransport{failN: 2, transport: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := Do(context.Background(), client, req, 5)
	if err != nil {
		t.Fatalf("expected Do to retry past connection errors and succeed, got: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if transport.calls != 3 {
		t.Fatalf("expected 3 RoundTrip calls (2 failures + 1 success), got %d", transport.calls)
	}
}

func TestDoGivesUpAfterMaxAttemptsOnConnectionError(t *testing.T) {
	transport := &flakyThenOKTransport{failN: 100, transport: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://example.invalid/unused", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Do(context.Background(), client, req, 3)
	if err == nil {
		t.Fatal("expected Do to give up and return an error after maxAttempts")
	}
	if transport.calls != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", transport.calls)
	}
}

// nonRetryableTransport always fails with a non-network error (a malformed
// request the transport itself rejects) — Do must not retry this.
type nonRetryableTransport struct{ calls int }

func (t *nonRetryableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	return nil, errors.New("unsupported protocol scheme")
}

func TestDoDoesNotRetryNonNetworkErrors(t *testing.T) {
	transport := &nonRetryableTransport{}
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://example.invalid/unused", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Do(context.Background(), client, req, 5)
	if err == nil {
		t.Fatal("expected Do to return an error")
	}
	if transport.calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a non-retryable error, got %d", transport.calls)
	}
}

func TestDoRespectsContextCancellationDuringNetworkRetryWait(t *testing.T) {
	transport := &flakyThenOKTransport{failN: 100, transport: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", "http://example.invalid/unused", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Cancel shortly after the first failure lands us in the retry wait —
	// Do must return promptly instead of sleeping out networkErrRetryWait.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err = Do(ctx, client, req, 5)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > networkErrRetryWait {
		t.Fatalf("expected Do to return promptly on cancellation, took %s", elapsed)
	}
}

func TestRetryAfterFallsBackToBodyRetryDelay(t *testing.T) {
	body := []byte(`{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"37s"}]}}`)
	resp := &http.Response{Header: make(http.Header)}
	if got, want := retryAfter(resp, body), 37*time.Second; got != want {
		t.Fatalf("retryAfter = %s, want %s", got, want)
	}
}

func TestRetryAfterHeaderTakesPriorityOverBody(t *testing.T) {
	body := []byte(`{"error":{"details":[{"retryDelay":"37s"}]}}`)
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"5"}}}
	if got, want := retryAfter(resp, body), 5*time.Second; got != want {
		t.Fatalf("retryAfter = %s, want %s", got, want)
	}
}

func TestRetryAfterDefaultsToOneSecond(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	if got, want := retryAfter(resp, nil), time.Second; got != want {
		t.Fatalf("retryAfter = %s, want %s", got, want)
	}
}
