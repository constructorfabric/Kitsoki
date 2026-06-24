package harscrub

import (
	"regexp"
	"strings"
	"testing"
)

const fixtureHAR = `{
  "log": {
    "version": "1.2",
    "creator": {"name": "test", "version": "1.0"},
    "entries": [
      {
        "startedDateTime": "2026-06-12T00:00:00Z",
        "time": 12.5,
        "request": {
          "method": "GET",
          "url": "https://api.example.com/v1/data?token=secret123&page=2&keep=ok",
          "headers": [
            {"name": "Authorization", "value": "Bearer abc.def.ghi"},
            {"name": "Cookie", "value": "session=xyz; theme=dark"},
            {"name": "Accept", "value": "application/json"}
          ],
          "queryString": [
            {"name": "token", "value": "secret123"},
            {"name": "page", "value": "2"},
            {"name": "keep", "value": "ok"}
          ],
          "postData": {
            "mimeType": "text/plain",
            "text": "config at /Users/someone/secret/key.pem mysecretvalue"
          }
        },
        "response": {
          "status": 200,
          "headers": [
            {"name": "Set-Cookie", "value": "sid=abc123; HttpOnly"},
            {"name": "Content-Type", "value": "application/json"}
          ],
          "content": {
            "size": 42,
            "mimeType": "application/json",
            "text": "{\"path\":\"/Users/someone/data.json\",\"leak\":\"mysecretvalue\"}"
          }
        }
      }
    ]
  }
}`

func scrubbedFixture(t *testing.T) *Har {
	t.Helper()
	h, err := ParseHar([]byte(fixtureHAR))
	if err != nil {
		t.Fatalf("ParseHar: %v", err)
	}
	Scrub(h, ScrubOptions{
		Home:           "/Users/someone",
		SecretPatterns: []*regexp.Regexp{regexp.MustCompile(`mysecretvalue`)},
	})
	return h
}

func headerValue(headers []NameValue, name string) string {
	for _, hd := range headers {
		if strings.EqualFold(hd.Name, name) {
			return hd.Value
		}
	}
	return ""
}

func queryValue(params []NameValue, name string) string {
	for _, p := range params {
		if strings.EqualFold(p.Name, name) {
			return p.Value
		}
	}
	return ""
}

func TestScrubHeaders(t *testing.T) {
	h := scrubbedFixture(t)
	req := h.Log.Entries[0].Request
	resp := h.Log.Entries[0].Response

	if got := headerValue(req.Headers, "Authorization"); got != Redacted {
		t.Errorf("Authorization = %q, want %q", got, Redacted)
	}
	if got := headerValue(req.Headers, "Cookie"); got != Redacted {
		t.Errorf("Cookie = %q, want %q", got, Redacted)
	}
	if got := headerValue(resp.Headers, "Set-Cookie"); got != Redacted {
		t.Errorf("Set-Cookie = %q, want %q", got, Redacted)
	}
	// Non-sensitive headers survive.
	if got := headerValue(req.Headers, "Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
	if got := headerValue(resp.Headers, "Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestScrubQueryParams(t *testing.T) {
	h := scrubbedFixture(t)
	req := h.Log.Entries[0].Request

	if got := queryValue(req.QueryString, "token"); got != Redacted {
		t.Errorf("queryString token = %q, want %q", got, Redacted)
	}
	if got := queryValue(req.QueryString, "page"); got != "2" {
		t.Errorf("queryString page = %q, want 2", got)
	}
	if got := queryValue(req.QueryString, "keep"); got != "ok" {
		t.Errorf("queryString keep = %q, want ok", got)
	}
}

func TestScrubURL(t *testing.T) {
	h := scrubbedFixture(t)
	url := h.Log.Entries[0].Request.URL

	if strings.Contains(url, "secret123") {
		t.Errorf("URL still contains token secret: %q", url)
	}
	if !strings.Contains(url, "token=%5BREDACTED%5D") && !strings.Contains(url, "token="+Redacted) {
		t.Errorf("URL token not redacted: %q", url)
	}
	// Non-sensitive params survive.
	if !strings.Contains(url, "page=2") {
		t.Errorf("URL lost page param: %q", url)
	}
	if !strings.Contains(url, "keep=ok") {
		t.Errorf("URL lost keep param: %q", url)
	}
}

func TestScrubHomePath(t *testing.T) {
	h := scrubbedFixture(t)
	entry := h.Log.Entries[0]

	if strings.Contains(entry.Request.PostData.Text, "/Users/someone") {
		t.Errorf("postData still has home path: %q", entry.Request.PostData.Text)
	}
	if !strings.Contains(entry.Request.PostData.Text, "$HOME/secret/key.pem") {
		t.Errorf("postData home not replaced: %q", entry.Request.PostData.Text)
	}
	if strings.Contains(entry.Response.Content.Text, "/Users/someone") {
		t.Errorf("response content still has home path: %q", entry.Response.Content.Text)
	}
	if !strings.Contains(entry.Response.Content.Text, "$HOME/data.json") {
		t.Errorf("response home not replaced: %q", entry.Response.Content.Text)
	}
}

func TestScrubSecretPatterns(t *testing.T) {
	h := scrubbedFixture(t)
	entry := h.Log.Entries[0]

	if strings.Contains(entry.Request.PostData.Text, "mysecretvalue") {
		t.Errorf("postData still contains secret: %q", entry.Request.PostData.Text)
	}
	if strings.Contains(entry.Response.Content.Text, "mysecretvalue") {
		t.Errorf("response still contains secret: %q", entry.Response.Content.Text)
	}
}

func TestNonSensitiveSurvives(t *testing.T) {
	h := scrubbedFixture(t)
	if h.Log.Version != "1.2" {
		t.Errorf("version = %q, want 1.2", h.Log.Version)
	}
	if h.Log.Entries[0].Time != 12.5 {
		t.Errorf("time = %v, want 12.5", h.Log.Entries[0].Time)
	}
	if h.Log.Entries[0].Response.Status != 200 {
		t.Errorf("status = %d, want 200", h.Log.Entries[0].Response.Status)
	}
	if h.Log.Entries[0].Request.Method != "GET" {
		t.Errorf("method = %q, want GET", h.Log.Entries[0].Request.Method)
	}
}

func TestRoundTrip(t *testing.T) {
	h := scrubbedFixture(t)
	out, err := Marshal(h)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(out), "secret123") ||
		strings.Contains(string(out), "mysecretvalue") ||
		strings.Contains(string(out), "/Users/someone") ||
		strings.Contains(string(out), "Bearer abc.def.ghi") {
		t.Errorf("marshaled output still contains sensitive data:\n%s", out)
	}
	if _, err := ParseHar(out); err != nil {
		t.Fatalf("re-parse failed: %v", err)
	}
}

func TestScrubNil(t *testing.T) {
	Scrub(nil, ScrubOptions{}) // must not panic
}

func TestDefaultSecretPatterns(t *testing.T) {
	opts := ScrubOptions{SecretPatterns: DefaultSecretPatterns()}

	// Each of these credential shapes must be redacted out of free text — this
	// is what protects console logs, error payloads, and serialized rrweb DOM
	// that the name-based header/query rules never reach.
	leaks := []string{
		"Authorization: Bearer abc.def.ghi123",
		"key " + "sk-ant-" + "api03-AAAA1111BBBB2222CCCC",
		"token " + "ghp_" + "0123456789abcdefghijABCDEFGHIJ012345",
		"AKIA" + "IOSFODNN7EXAMPLE here",
		"google " + "AIza" + "SyA1234567890abcdefghijklmnopqrstuv",
		"slack " + "xoxb-" + "1234567890-abcdefghijklmno",
		"jwt eyJhbGci.eyJzdWIiOiIx.SflKxwRJSME",
		`{"api_key": "deadbeefcafe1234"}`,
		"password=hunter2sekret",
		"-----BEGIN RSA PRIVATE KEY-----",
	}
	for _, in := range leaks {
		got := ScrubString(in, opts)
		if !strings.Contains(got, Redacted) {
			t.Errorf("ScrubString(%q) did not redact: %q", in, got)
		}
	}

	// Ordinary prose with no credential shape must survive untouched.
	clean := "the story advanced to step 3 and the badge turned green"
	if got := ScrubString(clean, opts); got != clean {
		t.Errorf("ScrubString mangled clean prose: %q -> %q", clean, got)
	}

	// DefaultSecretPatterns returns an independent copy each call (no shared
	// mutable slice handed to callers).
	a := DefaultSecretPatterns()
	if len(a) == 0 {
		t.Fatal("DefaultSecretPatterns returned empty set")
	}
	if &a[0] == &DefaultSecretPatterns()[0] {
		t.Error("DefaultSecretPatterns returned a shared backing array")
	}
}
