package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kevinnguyen/diaper-raffle/internal/store"
	"github.com/kevinnguyen/diaper-raffle/internal/webui"
)

// newLockedAPI builds a server with a shared password already set, and a
// client that keeps cookies the way a browser would.
func newLockedAPI(t *testing.T, password string) (*testAPI, *store.Store) {
	t.Helper()

	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if password != "" {
		if err := st.SetPassword(context.Background(), password); err != nil {
			t.Fatalf("set password: %v", err)
		}
	}

	assets, index, login, err := webui.Assets("")
	if err != nil {
		t.Fatalf("load assets: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(st, log, assets, index, login).Handler())
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	srv.Client().Jar = jar

	return &testAPI{t: t, server: srv}, st
}

// The whole point: without the password, nothing about the party is readable.
func TestLockedRaffleRefusesEverything(t *testing.T) {
	api, _ := newLockedAPI(t, "bottles")

	protected := []struct{ method, path string }{
		{http.MethodGet, "/api/events"},
		{http.MethodPost, "/api/events"},
		{http.MethodGet, "/api/events/anything"},
		{http.MethodPut, "/api/events/anything/roster"},
		{http.MethodPost, "/api/events/anything/draws"},
		{http.MethodGet, "/api/events/anything/draws"},
		{http.MethodGet, "/api/events/anything/odds"},
		{http.MethodGet, "/api/draws/1"},
		{http.MethodPost, "/api/draws/1/winners/0/reveal"},
	}

	for _, tc := range protected {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			status, body := api.do(tc.method, tc.path, nil)
			if status != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401", status)
			}
			if body["error"].(map[string]any)["code"] != "unauthorized" {
				t.Errorf("error code is %v", body["error"])
			}
		})
	}
}

// A browser asking for a page should get something it can type into, not JSON.
func TestLockedPageRoutesServeTheSignInPage(t *testing.T) {
	api, _ := newLockedAPI(t, "bottles")

	for _, path := range []string{"/", "/e/some-slug"} {
		res, err := api.server.Client().Get(api.server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()

		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s: got %d, want 401", path, res.StatusCode)
		}
		if !strings.Contains(string(body), "Ask whoever is running the shower") {
			t.Errorf("GET %s did not serve the sign-in page", path)
		}
		// And it must not have served the app.
		if strings.Contains(string(body), `id="roster"`) {
			t.Errorf("GET %s served the app to a signed-out caller", path)
		}
	}
}

func TestSignInThenUseTheRaffle(t *testing.T) {
	api, _ := newLockedAPI(t, "bottles and blankets")

	// Wrong password first.
	status, body := api.do(http.MethodPost, "/api/session", map[string]any{"password": "nope"})
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d, want 401", status)
	}
	if body["error"].(map[string]any)["message"] != "That is not the password." {
		t.Errorf("unhelpful message: %v", body["error"])
	}

	// Still locked out.
	if status, _ := api.do(http.MethodGet, "/api/events", nil); status != http.StatusUnauthorized {
		t.Fatalf("after a failed sign-in: got %d, want 401", status)
	}

	// Right password.
	status, body = api.do(http.MethodPost, "/api/session",
		map[string]any{"password": "bottles and blankets"})
	if status != http.StatusOK {
		t.Fatalf("correct password: got %d, want 200", status)
	}
	if body["authenticated"] != true {
		t.Errorf("not marked authenticated: %v", body)
	}

	// Now the raffle works, cookie carried by the jar.
	if status, _ := api.do(http.MethodGet, "/api/events", nil); status != http.StatusOK {
		t.Fatalf("after signing in: got %d, want 200", status)
	}

	slug := api.event(sampleRoster)
	status, body = api.do(http.MethodPost, "/api/events/"+slug+"/draws", nil)
	if status != http.StatusCreated {
		t.Fatalf("draw while signed in: got %d, want 201", status)
	}
	if len(body["draw"].(map[string]any)["winners"].([]any)) != 3 {
		t.Error("the draw did not work while signed in")
	}
}

func TestSignOut(t *testing.T) {
	api, _ := newLockedAPI(t, "bottles")

	api.do(http.MethodPost, "/api/session", map[string]any{"password": "bottles"})
	if status, _ := api.do(http.MethodGet, "/api/events", nil); status != http.StatusOK {
		t.Fatal("could not sign in")
	}

	if status, _ := api.do(http.MethodDelete, "/api/session", nil); status != http.StatusOK {
		t.Fatal("sign out failed")
	}
	if status, _ := api.do(http.MethodGet, "/api/events", nil); status != http.StatusUnauthorized {
		t.Error("still signed in after signing out")
	}
}

// Changing the password has to evict everybody already inside, or rotating it
// after an unwanted guest gets in would do nothing.
func TestChangingThePasswordSignsEverybodyOut(t *testing.T) {
	api, st := newLockedAPI(t, "bottles")

	api.do(http.MethodPost, "/api/session", map[string]any{"password": "bottles"})
	if status, _ := api.do(http.MethodGet, "/api/events", nil); status != http.StatusOK {
		t.Fatal("could not sign in")
	}

	if err := st.SetPassword(context.Background(), "blankets"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if err := st.RotateSessionKey(context.Background()); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	if status, _ := api.do(http.MethodGet, "/api/events", nil); status != http.StatusUnauthorized {
		t.Error("an old session survived a password change")
	}
}

// Guessing has to get expensive, since the password is a word said out loud.
func TestGuessingIsRateLimited(t *testing.T) {
	api, _ := newLockedAPI(t, "bottles")

	var limited bool
	for i := 0; i < maxAttempts+4; i++ {
		status, _ := api.do(http.MethodPost, "/api/session", map[string]any{"password": "wrong"})
		if status == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatalf("never rate limited after %d wrong guesses", maxAttempts+4)
	}

	// And the lockout holds even for the right password, so a guesser cannot
	// simply keep going once they stumble on it.
	status, _ := api.do(http.MethodPost, "/api/session", map[string]any{"password": "bottles"})
	if status != http.StatusTooManyRequests {
		t.Errorf("got %d during lockout, want 429", status)
	}
}

// An open raffle must keep working exactly as before.
func TestNoPasswordMeansNoGate(t *testing.T) {
	api, _ := newLockedAPI(t, "")

	if status, _ := api.do(http.MethodGet, "/api/events", nil); status != http.StatusOK {
		t.Error("an unlocked raffle refused a request")
	}

	res, err := api.server.Client().Get(api.server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `id="roster"`) {
		t.Error("an unlocked raffle did not serve the app")
	}

	status, sess := api.do(http.MethodGet, "/api/session", nil)
	if status != http.StatusOK || sess["locked"] != false {
		t.Errorf("session says %v", sess)
	}
}

// The health check has to stay reachable or the tunnel's monitor goes red.
func TestHealthAndStylesStayOpenWhenLocked(t *testing.T) {
	api, _ := newLockedAPI(t, "bottles")

	if status, body := api.do(http.MethodGet, "/api/health", nil); status != http.StatusOK {
		t.Errorf("health: got %d (%v), want 200", status, body)
	}

	res, err := api.server.Client().Get(api.server.URL + "/styles.css")
	if err != nil {
		t.Fatalf("GET /styles.css: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("stylesheet: got %d, want 200 — the sign-in page needs it", res.StatusCode)
	}
}

// The session endpoint tells the page what it is dealing with, without
// revealing anything about the party.
func TestSessionStatusWhenLocked(t *testing.T) {
	api, _ := newLockedAPI(t, "bottles")

	status, body := api.do(http.MethodGet, "/api/session", nil)
	if status != http.StatusOK {
		t.Fatalf("got %d, want 200", status)
	}
	if body["locked"] != true || body["authenticated"] != false {
		t.Errorf("session says %v", body)
	}
}

// A cookie somebody made up must not work.
func TestForgedCookieIsRejected(t *testing.T) {
	api, _ := newLockedAPI(t, "bottles")

	req, _ := http.NewRequest(http.MethodGet, api.server.URL+"/api/events", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "99999999999.forged"})

	res, err := api.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	res.Body.Close()

	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a forged cookie got %d, want 401", res.StatusCode)
	}
}
