package transport_test

import (
	"context"
	"encoding/base64"
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

func TestJiraTransport_NewRequiresFields(t *testing.T) {
	_, err := transport.NewJiraTransport(transport.JiraConfig{})
	require.Error(t, err)

	_, err = transport.NewJiraTransport(transport.JiraConfig{BaseURL: "https://x"})
	require.Error(t, err)

	_, err = transport.NewJiraTransport(transport.JiraConfig{BaseURL: "https://x", Username: "u"})
	require.Error(t, err)

	jt, err := transport.NewJiraTransport(transport.JiraConfig{
		BaseURL: "https://x", Username: "u", APIToken: "t",
	})
	require.NoError(t, err)
	require.Equal(t, "jira", jt.ID())
}

func TestJiraTransport_PostEncodesAndAuths(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":"42","self":"...","author":{}}`))
	}))
	t.Cleanup(srv.Close)

	jt, err := transport.NewJiraTransport(transport.JiraConfig{
		BaseURL:  srv.URL,
		Username: "alice@example.com",
		APIToken: "TOKEN",
	})
	require.NoError(t, err)

	id, err := jt.Post(context.Background(),
		transport.SessionKey{Transport: "jira", Thread: "PLTFRM-12345"},
		transport.Message{Title: "Reproduction complete", Body: "All steps documented."},
	)
	require.NoError(t, err)
	assert.Equal(t, "42", id)
	assert.Equal(t, "/rest/api/2/issue/PLTFRM-12345/comment", gotPath)
	assert.Equal(t, "application/json", gotCT)

	// Authorization: Basic base64(user:token)
	require.True(t, strings.HasPrefix(gotAuth, "Basic "), "Authorization should be Basic")
	expected := base64.StdEncoding.EncodeToString([]byte("alice@example.com:TOKEN"))
	assert.Equal(t, "Basic "+expected, gotAuth)

	body, _ := gotBody["body"].(string)
	assert.Contains(t, body, transport.DefaultBotMarker, "bot marker must be prefixed")
	assert.Contains(t, body, "*Reproduction complete*", "title rendered as Jira-wiki bold")
	assert.Contains(t, body, "All steps documented.")
}

func TestJiraTransport_CustomBotMarker(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"7"}`))
	}))
	t.Cleanup(srv.Close)

	jt, err := transport.NewJiraTransport(transport.JiraConfig{
		BaseURL:   srv.URL,
		Username:  "u",
		APIToken:  "t",
		BotMarker: "[kitsoki-test]",
	})
	require.NoError(t, err)

	_, err = jt.Post(context.Background(),
		transport.SessionKey{Transport: "jira", Thread: "X-1"},
		transport.Message{Body: "hi"},
	)
	require.NoError(t, err)

	body, _ := gotBody["body"].(string)
	assert.True(t, strings.HasPrefix(body, "[kitsoki-test] "), "custom bot marker not applied: %q", body)
}

func TestJiraTransport_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errorMessages":["You do not have permission to comment"]}`))
	}))
	t.Cleanup(srv.Close)

	jt, err := transport.NewJiraTransport(transport.JiraConfig{
		BaseURL: srv.URL, Username: "u", APIToken: "t",
	})
	require.NoError(t, err)

	_, err = jt.Post(context.Background(),
		transport.SessionKey{Transport: "jira", Thread: "X-1"},
		transport.Message{Body: "hi"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestJiraTransport_MissingThreadErrors(t *testing.T) {
	jt, err := transport.NewJiraTransport(transport.JiraConfig{
		BaseURL: "https://x", Username: "u", APIToken: "t",
	})
	require.NoError(t, err)

	_, err = jt.Post(context.Background(),
		transport.SessionKey{Transport: "jira", Thread: ""},
		transport.Message{Body: "hi"},
	)
	require.Error(t, err)
}

func TestJiraTransport_RegistersIntoRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"99"}`))
	}))
	t.Cleanup(srv.Close)

	jt, err := transport.NewJiraTransport(transport.JiraConfig{
		BaseURL: srv.URL, Username: "u", APIToken: "t",
	})
	require.NoError(t, err)

	r := transport.NewRegistry()
	r.Register(jt)
	t.Cleanup(func() { _ = r.Close() })

	id, err := r.Post(context.Background(),
		transport.SessionKey{Transport: "jira", Thread: "T-1"},
		transport.Message{Body: "registry-driven"},
	)
	require.NoError(t, err)
	assert.Equal(t, "99", id)
}
