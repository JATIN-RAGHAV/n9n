package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Password hashes are shared with the TypeScript Worker API
// (cloudflare/src/api/util.ts), so both servers can use one database. New
// hashes are PBKDF2-SHA256 stored as
// "pbkdf2_sha256$<iterations>$<salt base64>$<hash base64>"; Workers' Web
// Crypto allows at most 100,000 iterations, which is the default. bcrypt
// hashes written by earlier versions still verify.
const pbkdf2Prefix = "pbkdf2_sha256$"
const defaultPBKDF2Iterations = 100000

func pbkdf2Iterations() int {
	n, err := strconv.Atoi(os.Getenv("PASSWORD_PBKDF2_ITERATIONS"))
	if err != nil || n < 10000 || n > 100000 {
		return defaultPBKDF2Iterations
	}
	return n
}

func hashPassword(password string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	iterations := pbkdf2Iterations()
	hash, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return nil, err
	}
	enc := base64.StdEncoding
	return []byte(fmt.Sprintf("%s%d$%s$%s", pbkdf2Prefix, iterations, enc.EncodeToString(salt), enc.EncodeToString(hash))), nil
}

func verifyPassword(stored []byte, password string) bool {
	text := string(stored)
	if strings.HasPrefix(text, pbkdf2Prefix) {
		parts := strings.Split(text, "$")
		if len(parts) != 4 {
			return false
		}
		iterations, err := strconv.Atoi(parts[1])
		if err != nil || iterations < 1 || iterations > 100000 {
			return false
		}
		salt, err1 := base64.StdEncoding.DecodeString(parts[2])
		want, err2 := base64.StdEncoding.DecodeString(parts[3])
		if err1 != nil || err2 != nil {
			return false
		}
		got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
		return err == nil && subtle.ConstantTimeCompare(got, want) == 1
	}
	if strings.HasPrefix(text, "$2") {
		return bcrypt.CompareHashAndPassword(stored, []byte(password)) == nil
	}
	return false
}
