package transport_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/transport"
)

func TestBitbucketTransport_NewRequiresToken(t *testing.T) {
	_, err := transport.NewBitbucketTransport(transport.BitbucketConfig{})
	require.Error(t, err)

	bt, err := transport.NewBitbucketTransport(transport.BitbucketConfig{Token: "t"})
	require.NoError(t, err)
	require.Equal(t, "bitbucket", bt.ID())
}

func TestBitbucketTransport_PostEncodesAndAuths(t *testing.T) {
	var (
		gotPath string
		gotAuth string
		gotCT   string
		gotBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":12345,"version":0,"text":"…","author":{}}`))
	}))
	t.Cleanup(srv.Close)

	bt, err := transport.NewBitbucketTransport(transport.BitbucketConfig{
		BaseURL:    srv.URL,
		Token:      "MY-PAT",
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	id, err := bt.Post(context.Background(),
		transport.SessionKey{Transport: "bitbucket", Thread: "PLTFRM-89912"},
		transport.Message{
			Title: "Validate",
			Body:  "All checks pass.",
			Extra: map[string]string{
				"pr_project": "PLTFRM",
				"pr_slug":    "cyberstack",
				"pr_id":      "302",
			},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "12345", id)
	assert.Equal(t,
		"/rest/api/1.0/projects/PLTFRM/repos/cyberstack/pull-requests/302/comments",
		gotPath,
	)
	assert.Equal(t, "application/json", gotCT)
	assert.Equal(t, "Bearer MY-PAT", gotAuth)

	text, _ := gotBody["text"].(string)
	// Body JSON shape: `{"text": "[kitsoki] *<title>*\n\n<body>"}`.
	assert.Equal(t, "[kitsoki] *Validate*\n\nAll checks pass.", text)
}

func TestBitbucketTransport_CustomBotMarker(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7}`))
	}))
	t.Cleanup(srv.Close)

	bt, err := transport.NewBitbucketTransport(transport.BitbucketConfig{
		BaseURL:    srv.URL,
		Token:      "t",
		BotMarker:  "[kitsoki-test]",
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	_, err = bt.Post(context.Background(),
		transport.SessionKey{Transport: "bitbucket", Thread: "X-1"},
		transport.Message{
			Body: "hi",
			Extra: map[string]string{
				"pr_project": "P",
				"pr_slug":    "s",
				"pr_id":      "1",
			},
		},
	)
	require.NoError(t, err)
	text, _ := gotBody["text"].(string)
	assert.True(t, strings.HasPrefix(text, "[kitsoki-test] "), "custom bot marker not applied: %q", text)
}

func TestBitbucketTransport_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"message":"forbidden"}]}`))
	}))
	t.Cleanup(srv.Close)

	bt, err := transport.NewBitbucketTransport(transport.BitbucketConfig{
		BaseURL: srv.URL, Token: "t", HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	_, err = bt.Post(context.Background(),
		transport.SessionKey{Transport: "bitbucket", Thread: "X-1"},
		transport.Message{
			Body: "hi",
			Extra: map[string]string{
				"pr_project": "P", "pr_slug": "s", "pr_id": "1",
			},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "forbidden")
}

func TestBitbucketTransport_MissingCoordsErrors(t *testing.T) {
	bt, err := transport.NewBitbucketTransport(transport.BitbucketConfig{Token: "t"})
	require.NoError(t, err)

	// No Extra map at all.
	_, err = bt.Post(context.Background(),
		transport.SessionKey{Transport: "bitbucket", Thread: "X-1"},
		transport.Message{Body: "hi"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")

	// Partial Extra map (missing pr_id).
	_, err = bt.Post(context.Background(),
		transport.SessionKey{Transport: "bitbucket", Thread: "X-1"},
		transport.Message{
			Body: "hi",
			Extra: map[string]string{
				"pr_project": "P", "pr_slug": "s",
			},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pr_id")
}

func TestBitbucketTransport_RegistersIntoRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":99}`))
	}))
	t.Cleanup(srv.Close)

	bt, err := transport.NewBitbucketTransport(transport.BitbucketConfig{
		BaseURL: srv.URL, Token: "t", HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	r := transport.NewRegistry()
	r.Register(bt)
	t.Cleanup(func() { _ = r.Close() })

	id, err := r.Post(context.Background(),
		transport.SessionKey{Transport: "bitbucket", Thread: "T-1"},
		transport.Message{
			Body: "registry-driven",
			Extra: map[string]string{
				"pr_project": "P", "pr_slug": "s", "pr_id": "1",
			},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "99", id)
}
