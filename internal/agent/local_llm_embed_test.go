package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kitsoki/internal/embed"
)

// fakeEndpoint implements localSidecar for tests by returning a fixed base URL.
type fakeEndpoint struct{ base string }

func (f *fakeEndpoint) EnsureRunning(_ context.Context) (string, error) { return f.base, nil }
func (f *fakeEndpoint) Close() error                                    { return nil }

func TestLocalEmbedderBasic(t *testing.T) {
	// Canned response: 2 vectors, 4 dimensions each.
	canned := embedResponse{
		Data: []embedData{
			{Index: 0, Embedding: []float32{1, 0, 0, 0}},
			{Index: 1, Embedding: []float32{0, 1, 0, 0}},
		},
		Model: "test-model",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(canned)
	}))
	defer srv.Close()

	e := NewLocalEmbedder("test-model", &fakeEndpoint{base: srv.URL})
	vecs, err := e.Embed(context.Background(), []string{"hello", "world"}, embed.Document)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("want 2 vectors, got %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 4 {
			t.Errorf("vecs[%d]: want len 4, got %d", i, len(v))
		}
	}
}

func TestLocalEmbedderPrefixNomic(t *testing.T) {
	model := "nomic-embed-text-v1.5"

	tests := []struct {
		role       embed.Role
		wantPrefix string
	}{
		{embed.Document, "search_document: "},
		{embed.Query, "search_query: "},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.wantPrefix, func(t *testing.T) {
			var capturedInput []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var req embedRequest
				_ = json.Unmarshal(body, &req)
				capturedInput = req.Input

				resp := embedResponse{
					Data:  []embedData{{Index: 0, Embedding: []float32{1, 0, 0, 0}}},
					Model: model,
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer srv.Close()

			e := NewLocalEmbedder(model, &fakeEndpoint{base: srv.URL})
			_, err := e.Embed(context.Background(), []string{"apple"}, tc.role)
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}
			if len(capturedInput) == 0 {
				t.Fatal("server received no input")
			}
			if !strings.HasPrefix(capturedInput[0], tc.wantPrefix) {
				t.Errorf("want input[0] to start with %q, got %q", tc.wantPrefix, capturedInput[0])
			}
		})
	}
}

func TestLocalEmbedderPrefixBgeSmall(t *testing.T) {
	model := "bge-small-en-v1.5"

	tests := []struct {
		role       embed.Role
		wantPrefix string
		desc       string
	}{
		{embed.Document, "", "Document"},
		{embed.Query, "search_query: ", "Query"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			var capturedInput []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var req embedRequest
				_ = json.Unmarshal(body, &req)
				capturedInput = req.Input

				resp := embedResponse{
					Data:  []embedData{{Index: 0, Embedding: []float32{1, 0, 0, 0}}},
					Model: model,
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer srv.Close()

			e := NewLocalEmbedder(model, &fakeEndpoint{base: srv.URL})
			_, err := e.Embed(context.Background(), []string{"apple"}, tc.role)
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}
			if len(capturedInput) == 0 {
				t.Fatal("server received no input")
			}
			text := capturedInput[0]
			if tc.wantPrefix == "" {
				// Document role: no prefix, text should equal "apple".
				if text != "apple" {
					t.Errorf("Document role: want bare text %q, got %q", "apple", text)
				}
			} else {
				if !strings.HasPrefix(text, tc.wantPrefix) {
					t.Errorf("want input[0] to start with %q, got %q", tc.wantPrefix, text)
				}
			}
		})
	}
}
