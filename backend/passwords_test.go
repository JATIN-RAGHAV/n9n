package main

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Generated independently with Node's crypto.pbkdf2Sync, the same algorithm the
// TypeScript Worker uses through Web Crypto, so a hash written by either server
// verifies on the other.
const workerHash = "pbkdf2_sha256$100000$bjluLXRlc3Qtc2FsdC0xNg==$z23LpSETZQAIWkywjBGQ7I0716rz4/hnO69OOPXrLMo="

func TestPasswordHashFormatsShared(t *testing.T) {
	if !verifyPassword([]byte(workerHash), "smoke-password-123") {
		t.Fatal("PBKDF2 hash from the Worker format did not verify")
	}
	if verifyPassword([]byte(workerHash), "wrong-password-123") {
		t.Fatal("wrong password verified")
	}
	hash, err := hashPassword("another-password-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(hash), "pbkdf2_sha256$100000$") || !verifyPassword(hash, "another-password-1") {
		t.Fatalf("new hash %q has the wrong format or does not verify", hash)
	}
	legacy, _ := bcrypt.GenerateFromPassword([]byte("legacy-password-1"), bcrypt.MinCost)
	if !verifyPassword(legacy, "legacy-password-1") || verifyPassword(legacy, "nope-nope-nope") {
		t.Fatal("bcrypt hashes from older installs must still verify")
	}
	if verifyPassword([]byte("pbkdf2_sha256$999999$AA==$AA=="), "x") || verifyPassword([]byte("plain"), "plain") {
		t.Fatal("malformed hashes must not verify")
	}
}
