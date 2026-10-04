// Package auth holds the access password and the device logins.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// Auth checks the access password and the device tokens. Each row of
// device_logins holds the SHA-256 of the token text and of the access
// password, the same values as the Rust version writes.
type Auth struct {
	queries     *store.Queries
	fingerprint [sha256.Size]byte
	login       sync.Mutex
}

// Start deletes the device logins of each other access password.
func Start(ctx context.Context, queries *store.Queries, password string) (*Auth, error) {
	a := &Auth{queries: queries, fingerprint: sha256.Sum256([]byte(password))}
	if err := queries.DeleteOtherPasswordLogins(ctx, a.fingerprint[:]); err != nil {
		return nil, err
	}
	return a, nil
}

// Login gives the token of a new device login when password is the access password.
// A wrong password gives no token after one second.
func (a *Auth) Login(ctx context.Context, password, userAgent string) (string, bool, error) {
	a.login.Lock()
	defer a.login.Unlock()
	hash := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(hash[:], a.fingerprint[:]) != 1 {
		// The sleep holds the lock, so all clients together get at most one wrong guess each second.
		time.Sleep(time.Second)
		return "", false, nil
	}
	secret := make([]byte, 32)
	// rand.Read never returns an error.
	_, _ = rand.Read(secret)
	token := hex.EncodeToString(secret)
	tokenHash := sha256.Sum256([]byte(token))
	err := a.queries.AddDeviceLogin(ctx, store.AddDeviceLoginParams{
		TokenHash:           tokenHash[:],
		PasswordFingerprint: a.fingerprint[:],
		UserAgent:           userAgent,
		CreatedAt:           time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", false, err
	}
	return token, true, nil
}

// Check gives the device login of token.
func (a *Auth) Check(ctx context.Context, token string) (int64, bool, error) {
	hash := sha256.Sum256([]byte(token))
	id, err := a.queries.FindDeviceLogin(ctx, hash[:])
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// Logout deletes the device login with id.
func (a *Auth) Logout(ctx context.Context, id int64) error {
	return a.queries.DeleteDeviceLogin(ctx, id)
}
