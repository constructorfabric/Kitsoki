package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestKeyFile writes a throwaway RSA key (PKCS1 or PKCS8 PEM) and returns
// the path plus the key for verification.
func newTestKeyFile(t *testing.T, pkcs8 bool) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	var block *pem.Block
	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatalf("marshal pkcs8: %v", err)
		}
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	} else {
		block = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	}
	path := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path, key
}

func TestAppJWT(t *testing.T) {
	for _, pkcs8 := range []bool{false, true} {
		pkcs8 := pkcs8
		name := "pkcs1"
		if pkcs8 {
			name = "pkcs8"
		}
		t.Run(name, func(t *testing.T) {
			keyPath, key := newTestKeyFile(t, pkcs8)
			cfg := &Config{AppID: 12345, InstallationID: 999, PrivateKeyPath: keyPath}
			now := time.Unix(1_700_000_000, 0)

			tok, err := AppJWT(cfg, now)
			if err != nil {
				t.Fatalf("AppJWT: %v", err)
			}
			parts := strings.Split(tok, ".")
			if len(parts) != 3 {
				t.Fatalf("expected 3 JWT parts, got %d", len(parts))
			}

			// Header
			var hdr map[string]string
			decodePart(t, parts[0], &hdr)
			if hdr["alg"] != "RS256" || hdr["typ"] != "JWT" {
				t.Fatalf("bad header: %+v", hdr)
			}

			// Claims
			var claims map[string]any
			decodePart(t, parts[1], &claims)
			if iss, _ := claims["iss"].(float64); int64(iss) != cfg.AppID {
				t.Fatalf("iss=%v want %d", claims["iss"], cfg.AppID)
			}
			iat := int64(claims["iat"].(float64))
			exp := int64(claims["exp"].(float64))
			if iat != now.Add(-60*time.Second).Unix() {
				t.Fatalf("iat=%d want %d", iat, now.Add(-60*time.Second).Unix())
			}
			if exp != now.Add(9*time.Minute).Unix() {
				t.Fatalf("exp=%d want %d", exp, now.Add(9*time.Minute).Unix())
			}

			// Verify the signature with the corresponding PUBLIC key.
			signingInput := parts[0] + "." + parts[1]
			sig, err := base64.RawURLEncoding.DecodeString(parts[2])
			if err != nil {
				t.Fatalf("decode sig: %v", err)
			}
			digest := sha256.Sum256([]byte(signingInput))
			if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
				t.Fatalf("signature verification failed: %v", err)
			}
		})
	}
}

func decodePart(t *testing.T, part string, v any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		t.Fatalf("decode part: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal part: %v", err)
	}
}

// fakeDoer records requests and returns a canned response.
type fakeDoer struct {
	calls    int
	gotAuth  []string
	respBody string
	respCode int
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.calls++
	f.gotAuth = append(f.gotAuth, req.Header.Get("Authorization"))
	code := f.respCode
	if code == 0 {
		code = http.StatusCreated
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(f.respBody)),
		Header:     make(http.Header),
	}, nil
}

func TestAppTokenSource(t *testing.T) {
	keyPath, _ := newTestKeyFile(t, false)
	cfg := &Config{AppID: 42, InstallationID: 7, PrivateKeyPath: keyPath}

	expiry := time.Unix(1_700_003_600, 0).UTC()
	fake := &fakeDoer{respBody: `{"token":"ghs_installtoken","expires_at":"` + expiry.Format(time.RFC3339) + `"}`}

	src, err := NewAppTokenSource(cfg, fake)
	if err != nil {
		t.Fatalf("NewAppTokenSource: %v", err)
	}
	src.Now = func() time.Time { return time.Unix(1_700_000_000, 0) }

	tok, exp, err := src.InstallationToken(context.Background())
	if err != nil {
		t.Fatalf("InstallationToken: %v", err)
	}
	if tok != "ghs_installtoken" {
		t.Fatalf("token=%q", tok)
	}
	if !exp.Equal(expiry) {
		t.Fatalf("exp=%v want %v", exp, expiry)
	}
	if fake.calls != 1 {
		t.Fatalf("expected 1 Doer call, got %d", fake.calls)
	}
	// The Authorization header must be a Bearer App JWT (3-part token).
	auth := fake.gotAuth[0]
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Fatalf("auth=%q", auth)
	}
	if n := strings.Count(strings.TrimPrefix(auth, "Bearer "), "."); n != 2 {
		t.Fatalf("Authorization is not a JWT: %q", auth)
	}

	// Second call within expiry must NOT re-hit the Doer (cache).
	tok2, _, err := src.InstallationToken(context.Background())
	if err != nil {
		t.Fatalf("second InstallationToken: %v", err)
	}
	if tok2 != tok {
		t.Fatalf("cached token mismatch: %q vs %q", tok2, tok)
	}
	if fake.calls != 1 {
		t.Fatalf("expected cache hit (still 1 call), got %d", fake.calls)
	}

	// Advancing clock past expiry-skew forces a refresh.
	src.Now = func() time.Time { return expiry.Add(-30 * time.Second) }
	if _, _, err := src.InstallationToken(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if fake.calls != 2 {
		t.Fatalf("expected refresh (2 calls), got %d", fake.calls)
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "It's a Secret to Everybody"
	body := []byte("Hello, World!")
	// Known-good vector computed with the same HMAC-SHA256 construction.
	good := computeWebhookSignature(secret, body)

	cases := []struct {
		name   string
		secret string
		body   []byte
		sig    string
		want   bool
	}{
		{"valid", secret, body, good, true},
		{"tampered-body", secret, []byte("Hello, World?"), good, false},
		{"wrong-secret", "nope", body, good, false},
		{"missing-prefix", secret, body, strings.TrimPrefix(good, "sha256="), false},
		{"empty-secret", "", body, good, false},
		{"garbage", secret, body, "sha256=deadbeef", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerifyWebhookSignature(tc.secret, tc.body, tc.sig); got != tc.want {
				t.Fatalf("VerifyWebhookSignature=%v want %v", got, tc.want)
			}
		})
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Run("unset returns nil,nil", func(t *testing.T) {
		clearEnv(t)
		cfg, err := LoadConfigFromEnv()
		if err != nil || cfg != nil {
			t.Fatalf("got cfg=%v err=%v", cfg, err)
		}
	})
	t.Run("half-configured errors", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(EnvAppID, "1")
		cfg, err := LoadConfigFromEnv()
		if err == nil || cfg != nil {
			t.Fatalf("expected error, got cfg=%v err=%v", cfg, err)
		}
		if !strings.Contains(err.Error(), "half-configured") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("complete config", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(EnvAppID, "100")
		t.Setenv(EnvInstallationID, "200")
		t.Setenv(EnvPrivateKeyFile, "/path/to/key.pem")
		t.Setenv(EnvWebhookSecret, "shh")
		cfg, err := LoadConfigFromEnv()
		if err != nil {
			t.Fatalf("LoadConfigFromEnv: %v", err)
		}
		if cfg.AppID != 100 || cfg.InstallationID != 200 || cfg.PrivateKeyPath != "/path/to/key.pem" || cfg.WebhookSecret != "shh" {
			t.Fatalf("cfg=%+v", cfg)
		}
	})
	t.Run("non-integer app id", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(EnvAppID, "abc")
		t.Setenv(EnvInstallationID, "200")
		t.Setenv(EnvPrivateKeyFile, "/k.pem")
		if _, err := LoadConfigFromEnv(); err == nil {
			t.Fatal("expected parse error")
		}
	})
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvAppID, EnvInstallationID, EnvPrivateKeyFile, EnvWebhookSecret} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestStaticTokenSourceAndWithGHToken(t *testing.T) {
	os.Unsetenv("GH_TOKEN")
	ts := StaticTokenSource("ghp_dummy")
	var seen string
	err := WithGHToken(context.Background(), ts, func() error {
		seen = os.Getenv("GH_TOKEN")
		return nil
	})
	if err != nil {
		t.Fatalf("WithGHToken: %v", err)
	}
	if seen != "ghp_dummy" {
		t.Fatalf("GH_TOKEN inside fn=%q", seen)
	}
	if _, ok := os.LookupEnv("GH_TOKEN"); ok {
		t.Fatalf("GH_TOKEN should be unset after WithGHToken")
	}
}
