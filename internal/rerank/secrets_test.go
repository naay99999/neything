package rerank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rerankTestKey is deliberately distinctive so substring assertions against
// error strings are unambiguous.
const rerankTestKey = "AIzaTESTKEY123SECRET"

// TestRerankersDoNotLeakSecretsInErrors asserts that the error paths of every
// reranker stay secret-free. Cohere/Jina send their key in an Authorization
// header (never the URL), so the *url.Error a transport failure produces
// carries nothing sensitive; Ollama takes a user-supplied endpoint that it
// formats into its HTTP-status error, which is why that one is redacted.
func TestRerankersDoNotLeakSecretsInErrors(t *testing.T) {
	unreachable := "http://ney-rerank-leak-test.invalid"
	cands := []Candidate{{ChunkID: 1, Content: "doc"}}

	t.Run("cohere transport error", func(t *testing.T) {
		r := NewCohereReranker(rerankTestKey, "rerank-v3.5")
		r.BaseURL = unreachable
		_, err := r.Rerank(context.Background(), "q", cands)
		if err == nil {
			t.Fatal("expected a transport error")
		}
		if strings.Contains(err.Error(), rerankTestKey) {
			t.Fatalf("API key leaked into error: %v", err)
		}
	})

	t.Run("jina transport error", func(t *testing.T) {
		r := NewJinaReranker(rerankTestKey, "jina-reranker-v2-base-multilingual")
		r.BaseURL = unreachable
		_, err := r.Rerank(context.Background(), "q", cands)
		if err == nil {
			t.Fatal("expected a transport error")
		}
		if strings.Contains(err.Error(), rerankTestKey) {
			t.Fatalf("API key leaked into error: %v", err)
		}
	})

	// The local/Ollama reranker is the one that prints its request URL, so a
	// credential smuggled into the configured endpoint (query param or
	// userinfo — both are real gateway conventions) must be stripped.
	t.Run("ollama status error redacts endpoint credentials", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"nope"}`, http.StatusInternalServerError)
		}))
		defer srv.Close()

		// Endpoint carries credentials in userinfo (the form that survives
		// the constructor's `endpoint + "/v1/rerank"` concatenation); the
		// query-param form is covered by TestRedactURL below.
		endpoint := strings.Replace(srv.URL, "http://", "http://user:"+rerankTestKey+"@", 1)
		r := NewOllamaReranker(endpoint, "rerank-model")
		r.client = srv.Client()
		_, err := r.Rerank(context.Background(), "q", cands)
		if err == nil {
			t.Fatal("expected an error for HTTP 500")
		}
		if strings.Contains(err.Error(), rerankTestKey) {
			t.Fatalf("endpoint credential leaked into error: %v", err)
		}
	})
}

func TestRedactURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://host:1234/v1/rerank", "http://host:1234/v1/rerank"},
		{"http://host/v1/rerank?api-key=SECRET", "http://host/v1/rerank"},
		{"http://user:pw@host/v1/rerank?k=SECRET#frag", "http://host/v1/rerank"},
	}
	for _, c := range cases {
		if got := redactURL(c.in); got != c.want {
			t.Errorf("redactURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
