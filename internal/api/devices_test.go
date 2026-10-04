package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gork-labs/gork/pkg/api"
)

// login gives the request cookie of a new device login.
func login(t *testing.T, mux http.Handler, userAgent string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"correct horse"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login status = %d: %s", rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %v", cookies)
	}
	return sessionCookie + "=" + cookies[0].Value
}

func send(mux http.Handler, method, path, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type devices struct {
	Data struct {
		ThisDevice int64 `json:"thisDevice"`
		Logins     []struct {
			ID        int64  `json:"id"`
			UserAgent string `json:"userAgent"`
			CreatedAt string `json:"createdAt"`
		} `json:"logins"`
	} `json:"data"`
}

func listDevices(t *testing.T, mux http.Handler, cookie string) devices {
	t.Helper()
	rec := send(mux, http.MethodGet, "/api/devices", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("devices status = %d: %s", rec.Code, rec.Body)
	}
	var body devices
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestLoginSetsTheSessionCookieThatOpensTheAPI(t *testing.T) {
	mux, _, _ := newMux(t)
	started := time.Now()

	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"correct horse"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Firefox")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if !regexp.MustCompile(`^mobius_session=[0-9a-f]{64}; Path=/; Max-Age=34560000; HttpOnly; Secure; SameSite=Lax$`).MatchString(setCookie) {
		t.Errorf("Set-Cookie = %q", setCookie)
	}

	body := listDevices(t, mux, strings.SplitN(setCookie, ";", 2)[0])
	if len(body.Data.Logins) != 1 {
		t.Fatalf("logins = %+v", body.Data.Logins)
	}
	device := body.Data.Logins[0]
	if device.UserAgent != "Firefox" || body.Data.ThisDevice != device.ID {
		t.Errorf("devices = %+v", body.Data)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, device.CreatedAt)
	if err != nil || createdAt.Before(started.Add(-time.Second)) || createdAt.After(time.Now()) {
		t.Errorf("createdAt = %q, %v", device.CreatedAt, err)
	}
}

func TestLoginWithAWrongPasswordGives401AndTheMessage(t *testing.T) {
	mux, _, _ := newMux(t)
	started := time.Now()

	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"wrong horse"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
	if got, want := rec.Body.String(), `{"error":"`+wrongPassword+`"}`+"\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if cookie := rec.Header().Get("Set-Cookie"); cookie != "" {
		t.Errorf("Set-Cookie = %q", cookie)
	}
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Errorf("the wrong password took %v, want at least one second", elapsed)
	}
}

func TestDevicesListsTheNewestLoginFirst(t *testing.T) {
	mux, _, _ := newMux(t)
	login(t, mux, "Firefox")
	cookie := login(t, mux, "Safari")

	body := listDevices(t, mux, cookie)

	if len(body.Data.Logins) != 2 || body.Data.Logins[0].UserAgent != "Safari" || body.Data.Logins[1].UserAgent != "Firefox" {
		t.Errorf("logins = %+v", body.Data.Logins)
	}
	if body.Data.ThisDevice != body.Data.Logins[0].ID {
		t.Errorf("this device = %d", body.Data.ThisDevice)
	}
}

func TestLogoutEndsTheDeviceLogin(t *testing.T) {
	mux, _, _ := newMux(t)
	firefox := login(t, mux, "Firefox")
	safari := login(t, mux, "Safari")
	body := listDevices(t, mux, safari)

	rec := send(mux, http.MethodDelete, "/api/devices/"+strconv.FormatInt(body.Data.Logins[1].ID, 10), safari)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d: %s", rec.Code, rec.Body)
	}
	if rec := send(mux, http.MethodGet, "/api/devices", firefox); rec.Code != http.StatusUnauthorized {
		t.Errorf("status with the cookie of the logged out device = %d", rec.Code)
	}
	if body := listDevices(t, mux, safari); len(body.Data.Logins) != 1 || body.Data.Logins[0].UserAgent != "Safari" {
		t.Errorf("logins = %+v", body.Data.Logins)
	}
}

func TestEachAPIRouteButLoginNeedsTheSessionCookie(t *testing.T) {
	mux, _, _ := newMux(t)
	router := Routes(http.NewServeMux(), nil, nil)
	routes := router.GetRegistry().GetRoutes()
	if len(routes) < 2 {
		t.Fatalf("routes = %d", len(routes))
	}

	for _, route := range routes {
		if route.Method == http.MethodPost && route.Path == "/api/login" {
			continue
		}
		path := strings.ReplaceAll(route.Path, "{id}", "1")
		for _, cookie := range []string{"", sessionCookie + "=" + strings.Repeat("0", 64)} {
			rec := send(mux, route.Method, path, cookie)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with cookie %q: status = %d", route.Method, path, cookie, rec.Code)
			}
			if got, want := rec.Body.String(), `{"error":"`+noDeviceLogin+`"}`+"\n"; got != want {
				t.Errorf("%s %s: body = %q, want %q", route.Method, path, got, want)
			}
		}
	}
}

func TestSpecGivesTheSessionCookieAnd401ToEachRouteButLogin(t *testing.T) {
	router := Routes(http.NewServeMux(), nil, nil)
	spec := api.GenerateOpenAPI(router.GetRegistry())

	want := &api.SecurityScheme{Type: "apiKey", In: "cookie", Name: sessionCookie}
	if got := spec.Components.SecuritySchemes[sessionCookie]; !reflect.DeepEqual(got, want) {
		t.Errorf("security scheme = %+v, want %+v", got, want)
	}
	for _, route := range router.GetRegistry().GetRoutes() {
		item := spec.Paths[route.Path]
		operation := map[string]*api.Operation{http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodDelete: item.Delete}[route.Method]
		unauthorized := operation.Responses["401"]
		if unauthorized == nil || unauthorized.Content["application/json"].Schema.Ref != "#/components/schemas/ErrorResponse" {
			t.Errorf("%s %s: 401 response = %+v", route.Method, route.Path, unauthorized)
		}
		var wantSecurity []map[string][]string
		if route.Method != http.MethodPost || route.Path != "/api/login" {
			wantSecurity = []map[string][]string{{sessionCookie: {}}}
		}
		if !reflect.DeepEqual(operation.Security, wantSecurity) {
			t.Errorf("%s %s: security = %v, want %v", route.Method, route.Path, operation.Security, wantSecurity)
		}
	}
}
