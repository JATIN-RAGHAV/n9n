package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func (s *Server) oauth(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != "GET" {
		fail(w, 405, "method not allowed")
		return
	}
	if r.Header.Get("Authorization") != "" {
		fail(w, 401, "browser session required for Gmail connection")
		return
	}
	cookie, cookieErr := r.Cookie("n9n_session")
	if cookieErr != nil {
		fail(w, 401, "browser session required for Gmail connection")
		return
	}
	uid := s.user(r)
	if uid == "" {
		fail(w, 401, "authentication required")
		return
	}
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	secret := os.Getenv("GOOGLE_CLIENT_SECRET")
	redirect := os.Getenv("GOOGLE_REDIRECT_URI")
	if clientID == "" || secret == "" || redirect == "" {
		fail(w, 503, "Gmail connection not configured")
		return
	}
	sessionHash := sha256.Sum256([]byte(cookie.Value))
	sessionKey := hex.EncodeToString(sessionHash[:])
	switch action {
	case "start":
		state := id() + id()
		h := sha256.Sum256([]byte(state))
		_, e := s.db.Exec(`INSERT INTO oauth_states VALUES($1,$2,$3,$4)`, hex.EncodeToString(h[:]), uid, sessionKey, time.Now().UTC().Add(10*time.Minute).Format(timeLayout))
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		authorize := env("GOOGLE_AUTH_URL", "https://accounts.google.com/o/oauth2/v2/auth")
		u, e := url.Parse(authorize)
		if e != nil {
			fail(w, 500, "OAuth configuration error")
			return
		}
		q := u.Query()
		q.Set("client_id", clientID)
		q.Set("redirect_uri", redirect)
		q.Set("response_type", "code")
		q.Set("scope", "https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/gmail.send")
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
		q.Set("state", state)
		u.RawQuery = q.Encode()
		write(w, 200, map[string]string{"url": u.String()})
		return
	case "callback":
		state := r.URL.Query().Get("state")
		code := r.URL.Query().Get("code")
		if state == "" || code == "" {
			http.Redirect(w, r, "/#/credentials?error=gmail", http.StatusFound)
			return
		}
		h := sha256.Sum256([]byte(state))
		stateKey := hex.EncodeToString(h[:])
		tx, e := s.db.Begin()
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		var owner, storedSession, expires string
		e = tx.QueryRow(`SELECT user_id,session_hash,expires_at FROM oauth_states WHERE state_hash=$1 FOR UPDATE`, stateKey).Scan(&owner, &storedSession, &expires)
		if e != nil || owner != uid || storedSession != sessionKey || expires < now() {
			tx.Rollback()
			http.Redirect(w, r, "/#/credentials?error=gmail", http.StatusFound)
			return
		}
		_, e = tx.Exec(`DELETE FROM oauth_states WHERE state_hash=$1`, stateKey)
		if e != nil {
			tx.Rollback()
			fail(w, 500, "database error")
			return
		}
		if e = tx.Commit(); e != nil {
			fail(w, 500, "database error")
			return
		}
		endpoint := env("GOOGLE_TOKEN_URL", "https://oauth2.googleapis.com/token")
		form := url.Values{"client_id": {clientID}, "client_secret": {secret}, "code": {code}, "grant_type": {"authorization_code"}, "redirect_uri": {redirect}}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		req, e := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
		if e != nil {
			fail(w, 500, "OAuth request error")
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			http.Redirect(w, r, "/#/credentials?error=gmail", http.StatusFound)
			return
		}
		defer resp.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if e != nil || resp.StatusCode != 200 {
			http.Redirect(w, r, "/#/credentials?error=gmail", http.StatusFound)
			return
		}
		var token struct {
			RefreshToken string `json:"refresh_token"`
		}
		if json.Unmarshal(raw, &token) != nil || token.RefreshToken == "" {
			http.Redirect(w, r, "/#/credentials?error=gmail", http.StatusFound)
			return
		}
		ciphertext, e := s.encrypt(map[string]string{"client_id": clientID, "client_secret": secret, "refresh_token": token.RefreshToken})
		if e != nil {
			fail(w, 500, "encryption error")
			return
		}
		_, e = s.db.Exec(`INSERT INTO credentials VALUES($1,$2,$3,$4,$5,$6)`, id(), uid, "Gmail", "gmail_oauth", ciphertext, now())
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		http.Redirect(w, r, "/#/credentials?connected=gmail", http.StatusFound)
		return
	}
	fail(w, 404, "not found")
}
