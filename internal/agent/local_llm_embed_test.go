package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"kitsoki/internal/embed"
)

// fakeEndpoint implements localSidecar for tests by returning a fixed base URL.
type fakeEndpoint struct{ base string }

func (f *fakeEndpoint) EnsureRunning(_ context.Context) (string, error) { return f.base, nil }
func (f *fakeEndpoint) Close() error                                    { return nil }

func newLocalEmbedderForTest(model string, rt http.RoundTripper) *LocalEmbedder {
	return NewLocalEmbedder(model, &fakeEndpoint{base: "https://embed.test"}).
		WithHTTPClient(&http.Client{Transport: rt})
}

func TestLocalEmbedderBasic(t *testing.T) {
	// Canned response: 2 vectors, 4 dimensions each.
	canned := embedResponse{
		Data: []embedData{
			{Index: 0, Embedding: []float32{1, 0, 0, 0}},
			{Index: 1, Embedding: []float32{0, 1, 0, 0}},
		},
		Model: "test-model",
	}
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/embeddings" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Status:     "404 Not Found",
				Body:       io.NopCloser(strings.NewReader("not found")),
				Request:    r,
			}, nil
		}
		raw, _ := json.Marshal(canned)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(raw))),
			Request:    r,
		}, nil
	})

	e := newLocalEmbedderForTest("test-model", rt)
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
			rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				var req embedRequest
				_ = json.Unmarshal(body, &req)
				capturedInput = req.Input

				resp := embedResponse{
					Data:  []embedData{{Index: 0, Embedding: []float32{1, 0, 0, 0}}},
					Model: model,
				}
				raw, _ := json.Marshal(resp)
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(string(raw))),
					Request:    r,
				}, nil
			})

			e := newLocalEmbedderForTest(model, rt)
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
			rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				var req embedRequest
				_ = json.Unmarshal(body, &req)
				capturedInput = req.Input

				resp := embedResponse{
					Data:  []embedData{{Index: 0, Embedding: []float32{1, 0, 0, 0}}},
					Model: model,
				}
				raw, _ := json.Marshal(resp)
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(string(raw))),
					Request:    r,
				}, nil
			})

			e := newLocalEmbedderForTest(model, rt)
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
