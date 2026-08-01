package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testAPIKey is deliberately distinctive so a substring assertion against an
// error string is unambiguous — no chance of an incidental match.
const testAPIKey = "AIzaTESTKEY123SECRET"

// TestGeminiEmbedder_APIKeyNotInURL pins the fix for the leak: the key must
// travel in the x-goog-api-key header, never the `?key=` query param Google
// also accepts. A URL-embedded key ends up in every *url.Error the transport
// produces (Go redacts userinfo passwords but never query params), and those
// errors are printed verbatim by `ney index`, `ney mcp` and `ney doctor`.
func TestGeminiEmbedder_APIKeyNotInURL(t *testing.T) {
	var gotURL, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotHeader = r.Header.Get("x-goog-api-key")
		json.NewEncoder(w).Encode(map[string]any{
			"embeddings": []map[string]any{{"values": []float32{0.1, 0.2}}},
		})
	}))
	defer srv.Close()

	e := NewGeminiEmbedder(testAPIKey, "text-embedding-004")
	e.BaseURL = srv.URL
	e.client = srv.Client()

	if _, err := e.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if gotHeader != testAPIKey {
		t.Errorf("x-goog-api-key header = %q, want the API key", gotHeader)
	}
	if strings.Contains(gotURL, testAPIKey) {
		t.Errorf("request URL %q contains the API key", gotURL)
	}
	if strings.Contains(gotURL, "key=") {
		t.Errorf("request URL %q still uses the ?key= query param", gotURL)
	}
}

// TestGeminiEmbedder_TransportErrorDoesNotLeakAPIKey is the defensive
// regression: point the embedder at a host that can't resolve, force the
// transport error path, and assert the key isn't anywhere in the resulting
// message. The `.invalid` TLD is reserved (RFC 2606) and never resolves, and
// a DNS "no such host" is non-retryable in apiretry so this returns at once.
func TestGeminiEmbedder_TransportErrorDoesNotLeakAPIKey(t *testing.T) {
	e := NewGeminiEmbedder(testAPIKey, "text-embedding-004")
	e.BaseURL = "http://ney-gemini-leak-test.invalid"

	_, err := e.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected a transport error against an unresolvable host")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("API key leaked into error: %v", err)
	}
}

// TestGeminiEmbedder_HTTPErrorDoesNotLeakAPIKey covers the other error path
// out of embedBatch — a non-200 whose body is echoed via errBodySnippet.
// The status error must identify the failure without carrying the key.
func TestGeminiEmbedder_HTTPErrorDoesNotLeakAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"API key not valid"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	e := NewGeminiEmbedder(testAPIKey, "text-embedding-004")
	e.BaseURL = srv.URL
	e.client = srv.Client()

	_, err := e.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected an error for HTTP 400")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("API key leaked into error: %v", err)
	}
}

// TestOpenAIEmbedder_TransportErrorDoesNotLeakAPIKey guards the sibling
// provider against regressing into the same pattern — it currently sends the
// key in an Authorization header, and this locks that in.
func TestOpenAIEmbedder_TransportErrorDoesNotLeakAPIKey(t *testing.T) {
	e := NewOpenAIEmbedder(testAPIKey, "text-embedding-3-small")
	e.BaseURL = "http://ney-openai-leak-test.invalid"

	_, err := e.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected a transport error against an unresolvable host")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("API key leaked into error: %v", err)
	}
}
