package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mobileRequest(t *testing.T, s *Server, method, path, bearer string, cookie *http.Cookie, body any) (int, map[string]any, []*http.Cookie) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		r.Header.Set("Authorization", bearer)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var value map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return w.Code, value, w.Result().Cookies()
}

func TestMobileSessionAndBrowserIsolation(t *testing.T) {
	s := testServer(t)
	credentials := map[string]any{"email": "mobile@example.com", "password": "mobile-password-123"}
	code, result, cookies := mobileRequest(t, s, "POST", "/api/auth/mobile/register", "", nil, credentials)
	requireOK(t, code, result)
	if len(cookies) != 0 {
		t.Fatalf("mobile registration set cookies: %v", cookies)
	}
	user := result["user"].(map[string]any)
	if user["email"] != "mobile@example.com" {
		t.Fatalf("user: %v", user)
	}
	token := result["token"].(string)
	if len(token) != 64 {
		t.Fatalf("token length %d", len(token))
	}
	expiry, err := time.Parse(time.RFC3339Nano, result["expires_at"].(string))
	if err != nil || time.Until(expiry) < 29*24*time.Hour {
		t.Fatalf("expiry %v %v", expiry, err)
	}
	hash := sha256.Sum256([]byte(token))
	var stored string
	if err := s.db.QueryRow(`SELECT token_hash FROM sessions WHERE user_id=$1`, user["id"]).Scan(&stored); err != nil || stored != hex.EncodeToString(hash[:]) || stored == token {
		t.Fatalf("session not stored hashed: %q %v", stored, err)
	}
	code, result, _ = mobileRequest(t, s, "GET", "/api/auth/me", "Bearer "+token, nil, nil)
	requireOK(t, code, result)
	code, result, _ = mobileRequest(t, s, "GET", "/api/nodes", "Bearer "+token, nil, nil)
	requireOK(t, code, result)
	code, _, _ = mobileRequest(t, s, "GET", "/api/auth/me", "Bearer "+testToken, nil, nil)
	if code != 401 {
		t.Fatalf("runner token accepted as user: %d", code)
	}
	code, _, _ = mobileRequest(t, s, "GET", "/api/auth/me", "Bearer invalid", nil, nil)
	if code != 401 {
		t.Fatalf("invalid bearer accepted: %d", code)
	}

	code, result, browserCookie := request(t, s, nil, "POST", "/api/auth/login", credentials, false)
	requireOK(t, code, result)
	if browserCookie == nil {
		t.Fatal("browser login did not set cookie")
	}
	code, _, _ = mobileRequest(t, s, "GET", "/api/auth/me", "Bearer invalid", browserCookie, nil)
	if code != 401 {
		t.Fatalf("invalid bearer fell back to cookie: %d", code)
	}
	code, _, _ = mobileRequest(t, s, "GET", "/api/auth/me", "Basic anything", browserCookie, nil)
	if code != 401 {
		t.Fatalf("wrong auth scheme fell back to cookie: %d", code)
	}
	code, result, cookies = mobileRequest(t, s, "POST", "/api/auth/logout", "Bearer "+token, browserCookie, map[string]any{})
	requireOK(t, code, result)
	if len(cookies) != 0 {
		t.Fatalf("bearer logout changed browser cookie: %v", cookies)
	}
	code, _, _ = mobileRequest(t, s, "GET", "/api/auth/me", "Bearer "+token, nil, nil)
	if code != 401 {
		t.Fatalf("revoked bearer remained valid: %d", code)
	}
	code, result, _ = mobileRequest(t, s, "GET", "/api/auth/me", "", browserCookie, nil)
	requireOK(t, code, result)
	code, result, cookies = mobileRequest(t, s, "POST", "/api/auth/mobile/login", "", nil, credentials)
	requireOK(t, code, result)
	if len(cookies) != 0 || result["token"] == token {
		t.Fatalf("mobile login did not issue independent bearer session: %v %v", cookies, result)
	}
	newToken := result["token"].(string)
	code, _, _ = mobileRequest(t, s, "POST", "/api/auth/logout", "Bearer "+newToken, nil, map[string]any{})
	if code != 200 {
		t.Fatalf("mobile logout: %d", code)
	}
	code, _, _ = mobileRequest(t, s, "GET", "/api/auth/me", "Bearer "+newToken, nil, nil)
	if code != 401 {
		t.Fatalf("second token not revoked: %d", code)
	}
	code, _, _ = mobileRequest(t, s, "POST", "/api/auth/mobile/login/extra", "", nil, credentials)
	if code != 404 {
		t.Fatalf("unknown mobile auth path: %d", code)
	}
	if strings.Contains(newToken, token) {
		t.Fatal("session token reused")
	}
}

func TestMobileTokenExpiryAndNoStore(t *testing.T) {
	s := testServer(t)
	body := []byte(`{"email":"expiry@example.com","password":"expiry-password-123"}`)
	r := httptest.NewRequest("POST", "/api/auth/mobile/register", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("registration status=%d cache=%q", w.Code, w.Header().Get("Cache-Control"))
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	token := result["token"].(string)
	hash := sha256.Sum256([]byte(token))
	if _, err := s.db.Exec(`UPDATE sessions SET expires_at=$1 WHERE token_hash=$2`, "2000-01-01T00:00:00.000000000Z", hex.EncodeToString(hash[:])); err != nil {
		t.Fatal(err)
	}
	code, _, _ := mobileRequest(t, s, "GET", "/api/auth/me", "Bearer "+token, nil, nil)
	if code != 401 {
		t.Fatalf("expired bearer accepted: %d", code)
	}
}
