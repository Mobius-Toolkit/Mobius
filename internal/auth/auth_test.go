package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

func start(t *testing.T, path, password string) *Auth {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := Start(context.Background(), store.New(db), password)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func login(t *testing.T, a *Auth) string {
	t.Helper()
	token, ok, err := a.Login(context.Background(), "correct horse", "Firefox")
	if err != nil || !ok {
		t.Fatalf("login: ok = %v, err = %v", ok, err)
	}
	return token
}

func check(t *testing.T, a *Auth, token string) (int64, bool) {
	t.Helper()
	id, ok, err := a.Check(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return id, ok
}

func TestLoginGivesATokenThatCheckAccepts(t *testing.T) {
	a := start(t, filepath.Join(t.TempDir(), "mobius.db"), "correct horse")

	token := login(t, a)

	if _, ok := check(t, a, token); !ok {
		t.Error("check refuses the token")
	}
	if _, ok := check(t, a, token+"0"); ok {
		t.Error("check accepts another token")
	}
}

func TestLoginWithAWrongPasswordFailsAfterOneSecond(t *testing.T) {
	a := start(t, filepath.Join(t.TempDir(), "mobius.db"), "correct horse")
	started := time.Now()

	token, ok, err := a.Login(context.Background(), "battery staple", "Firefox")

	if err != nil || ok || token != "" {
		t.Errorf("login = %q, %v, %v", token, ok, err)
	}
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Errorf("login took %s", elapsed)
	}
}

func TestLogoutMakesCheckRefuseTheToken(t *testing.T) {
	a := start(t, filepath.Join(t.TempDir(), "mobius.db"), "correct horse")
	token := login(t, a)
	other := login(t, a)
	id, _ := check(t, a, token)

	if err := a.Logout(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	if _, ok := check(t, a, token); ok {
		t.Error("check accepts the token after the logout")
	}
	if _, ok := check(t, a, other); !ok {
		t.Error("check refuses the token of the other device")
	}
}

func TestStartKeepsTheLoginsUntilTheAccessPasswordChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobius.db")
	token := login(t, start(t, path, "correct horse"))

	if _, ok := check(t, start(t, path, "correct horse"), token); !ok {
		t.Error("check refuses the token after a start with the same password")
	}
	if _, ok := check(t, start(t, path, "battery staple"), token); ok {
		t.Error("check accepts the token after a start with another password")
	}
}

// The Rust version writes SHA-256 of the token text and of the access password.
func TestCheckAcceptsALoginOfTheRustVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobius.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`INSERT INTO device_logins (token_hash, password_fingerprint, user_agent, created_at) VALUES (
		X'a8ae6e6ee929abea3afcfc5258c8ccd6f85273e0d4626d26c7279f3250f77c8e',
		X'4104d36f8da2c254349f85836793ebe029e0c957063a34c91c2e9203187b5631',
		'Firefox', '2026-09-28T19:35:54.52055Z')`)
	if err != nil {
		t.Fatal(err)
	}

	a := start(t, path, "correct horse")

	if id, ok := check(t, a, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); !ok || id != 1 {
		t.Errorf("check = %d, %v; want 1, true", id, ok)
	}
}
