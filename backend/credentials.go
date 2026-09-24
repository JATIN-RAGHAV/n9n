package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func (s *Server) encrypt(data any) (string, error) {
	block, e := aes.NewCipher(s.key[:])
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return "", e
	}
	raw, _ := json.Marshal(data)
	return base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, raw, nil)), nil
}
func (s *Server) decrypt(value string) (any, error) {
	raw, e := base64.StdEncoding.DecodeString(value)
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(s.key[:])
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	if len(raw) < g.NonceSize() {
		return nil, errors.New("bad ciphertext")
	}
	plain, e := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], nil)
	if e != nil {
		return nil, e
	}
	var v any
	e = json.Unmarshal(plain, &v)
	return v, e
}
func (s *Server) credentials(w http.ResponseWriter, r *http.Request, uid string, p []string) {
	if len(p) == 0 {
		switch r.Method {
		case "GET":
			rows, e := s.db.Query(`SELECT id,name,kind,created_at FROM credentials WHERE user_id=$1 ORDER BY created_at DESC`, uid)
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			defer rows.Close()
			list := []map[string]string{}
			for rows.Next() {
				var id, name, kind, created string
				if rows.Scan(&id, &name, &kind, &created) == nil {
					list = append(list, map[string]string{"id": id, "name": name, "kind": kind, "created_at": created})
				}
			}
			write(w, 200, map[string]any{"credentials": list})
			return
		case "POST":
			var a struct {
				Name string         `json:"name"`
				Kind string         `json:"kind"`
				Data map[string]any `json:"data"`
			}
			if e := decode(r, &a); e != nil {
				fail(w, 400, e.Error())
				return
			}
			a.Name = strings.TrimSpace(a.Name)
			a.Kind = strings.TrimSpace(a.Kind)
			if a.Name == "" || a.Kind == "" || len(a.Name) > 200 || len(a.Kind) > 100 || a.Data == nil {
				fail(w, 400, "invalid credential")
				return
			}
			ciphertext, e := s.encrypt(a.Data)
			if e != nil {
				fail(w, 500, "encryption error")
				return
			}
			id := id()
			created := now()
			_, e = s.db.Exec(`INSERT INTO credentials VALUES($1,$2,$3,$4,$5,$6)`, id, uid, a.Name, a.Kind, ciphertext, created)
			if e != nil {
				fail(w, 500, "database error")
				return
			}
			write(w, 200, map[string]any{"credential": map[string]string{"id": id, "name": a.Name, "kind": a.Kind, "created_at": created}})
			return
		}
	}
	if len(p) == 1 && r.Method == "DELETE" {
		res, e := s.db.Exec(`DELETE FROM credentials WHERE id=$1 AND user_id=$2`, p[0], uid)
		if e != nil {
			fail(w, 500, "database error")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 404, "credential not found")
			return
		}
		write(w, 200, map[string]bool{"ok": true})
		return
	}
	fail(w, 404, "not found")
}
func (s *Server) credentialForJob(w http.ResponseWriter, runID, credID, lease string) {
	var graphStr, ciphertext, name, kind string
	var until string
	e := s.db.QueryRow(`SELECT v.graph,r.lease_until,c.ciphertext,c.name,c.kind FROM runs r JOIN versions v ON v.workflow_id=r.workflow_id AND v.version=r.version JOIN workflows w ON w.id=r.workflow_id JOIN credentials c ON c.id=$1 AND c.user_id=w.user_id WHERE r.id=$2 AND r.status='running' AND r.lease_token=$3`, credID, runID, lease).Scan(&graphStr, &until, &ciphertext, &name, &kind)
	if e != nil || until < now() {
		fail(w, 403, "invalid job lease or credential")
		return
	}
	var graph Graph
	_ = json.Unmarshal([]byte(graphStr), &graph)
	referenced := false
	for _, n := range graph.Nodes {
		if n.CredentialID == credID {
			referenced = true
			break
		}
	}
	if !referenced {
		fail(w, 403, "credential not in workflow")
		return
	}
	data, e := s.decrypt(ciphertext)
	if e != nil {
		fail(w, 500, "decryption error")
		return
	}
	write(w, 200, map[string]any{"credential": map[string]any{"id": credID, "name": name, "kind": kind, "data": data}})
}
func (s *Server) credentialForTrigger(w http.ResponseWriter, wid, credID string, version int) {
	var graphStr, ciphertext, name, kind string
	e := s.db.QueryRow(`SELECT v.graph,c.ciphertext,c.name,c.kind FROM workflows w JOIN versions v ON v.workflow_id=w.id AND v.version=w.published_version JOIN credentials c ON c.id=$1 AND c.user_id=w.user_id WHERE w.id=$2 AND w.active=TRUE AND v.version=$3`, credID, wid, version).Scan(&graphStr, &ciphertext, &name, &kind)
	if e != nil {
		fail(w, 403, "trigger credential unavailable")
		return
	}
	var graph Graph
	_ = json.Unmarshal([]byte(graphStr), &graph)
	referenced := false
	for _, n := range graph.Nodes {
		if n.Type == "email_trigger" && n.CredentialID == credID {
			referenced = true
			break
		}
	}
	if !referenced {
		fail(w, 403, "credential not in trigger")
		return
	}
	data, e := s.decrypt(ciphertext)
	if e != nil {
		fail(w, 500, "decryption error")
		return
	}
	write(w, 200, map[string]any{"credential": map[string]any{"id": credID, "name": name, "kind": kind, "data": data}})
}
func equalSecret(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
