package service

import "testing"

func TestCSRFTokensAreSessionBoundAndTamperEvident(t *testing.T) {
	auth := NewAuthService(nil, "unit-test-secret")
	token, err := auth.NewCSRFToken("session-one")
	if err != nil {
		t.Fatalf("NewCSRFToken() error = %v", err)
	}
	if !auth.VerifyCSRFToken("session-one", token) {
		t.Fatal("valid CSRF token rejected")
	}
	if auth.VerifyCSRFToken("session-two", token) {
		t.Fatal("CSRF token unexpectedly valid for another session")
	}
	replacement := "A"
	if token[len(token)-1] == 'A' {
		replacement = "B"
	}
	tampered := token[:len(token)-1] + replacement
	if auth.VerifyCSRFToken("session-one", tampered) {
		t.Fatal("tampered CSRF token accepted")
	}
}
