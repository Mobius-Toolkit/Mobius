package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gork-labs/gork/pkg/api"
)

const (
	sessionCookie = "mobius_session"
	// sessionMaxAge is 400 days in seconds, the longest cookie life that browsers keep.
	sessionMaxAge = 400 * 24 * 60 * 60

	noDeviceLogin = "The request has no device login."
	wrongPassword = "The access password is wrong."
)

type deviceKey struct{}

func (h *handlers) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, noDeviceLogin)
			return
		}
		device, ok, err := h.auth.Check(r.Context(), cookie.Value)
		if err != nil {
			log.Printf("check device login: %v", err)
			writeError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, noDeviceLogin)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceKey{}, device)))
	})
}

// writeError writes an error response in the format of Gork.
func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(api.ErrorResponse{Error: message})
}

// LoginRequest is the request of Login.
type LoginRequest struct {
	Headers struct {
		// UserAgent names the browser of the device
		UserAgent string `gork:"User-Agent"`
	}
	Body struct {
		// Password is the access password
		Password string `gork:"password" validate:"required"`
	}
}

// LoginResponse is the response of Login.
type LoginResponse struct {
	Cookies struct {
		// Session holds the token of the device login
		Session *http.Cookie
	}
}

// Login makes a device login when the password is the access password,
// and sets the token of the device login in a cookie.
func (h *handlers) Login(ctx context.Context, req LoginRequest) (*LoginResponse, error) {
	token, ok, err := h.auth.Login(ctx, req.Body.Password, req.Headers.UserAgent)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.NewHTTPError(http.StatusUnauthorized, wrongPassword)
	}
	resp := &LoginResponse{}
	resp.Cookies.Session = &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAge,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	return resp, nil
}

// ListDevicesRequest is the request of ListDevices.
type ListDevicesRequest struct{}

// DeviceLogin is the login of a device with the access password.
type DeviceLogin struct {
	// ID is the id of the device login
	ID int64 `gork:"id"`
	// UserAgent names the browser of the device
	UserAgent string `gork:"userAgent"`
	// CreatedAt is the time of the login
	CreatedAt time.Time `gork:"createdAt"`
}

// Devices are the device logins.
type Devices struct {
	// ThisDevice is the id of the device login of the request
	ThisDevice int64 `gork:"thisDevice"`
	// Logins are the device logins, the newest first
	Logins []DeviceLogin `gork:"logins"`
}

// ListDevicesResponse is the response of ListDevices.
type ListDevicesResponse struct {
	Body Envelope[Devices]
}

// ListDevices returns the device logins.
func (h *handlers) ListDevices(ctx context.Context, _ ListDevicesRequest) (*ListDevicesResponse, error) {
	rows, err := h.queries.ListDeviceLogins(ctx)
	if err != nil {
		return nil, err
	}
	logins := make([]DeviceLogin, 0, len(rows))
	for _, row := range rows {
		createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
		if err != nil {
			return nil, err
		}
		logins = append(logins, DeviceLogin{ID: row.ID, UserAgent: row.UserAgent, CreatedAt: createdAt})
	}
	devices := Devices{ThisDevice: ctx.Value(deviceKey{}).(int64), Logins: logins}
	return &ListDevicesResponse{Body: Envelope[Devices]{Data: devices}}, nil
}

// LogoutRequest is the request of Logout.
type LogoutRequest struct {
	Path struct {
		// ID is the id of the device login
		ID int64 `gork:"id"`
	}
}

// Logout deletes a device login.
func (h *handlers) Logout(ctx context.Context, req LogoutRequest) error {
	return h.auth.Logout(ctx, req.Path.ID)
}
