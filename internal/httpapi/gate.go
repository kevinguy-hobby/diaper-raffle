package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kevinnguyen/diaper-raffle/internal/auth"
)

const (
	sessionCookie = "raffle_session"

	// SessionTTL is how long one sign-in lasts. A shower runs an afternoon;
	// a week means nobody gets bounced mid-party and the host does not have to
	// re-announce the password after a nap.
	SessionTTL = 7 * 24 * time.Hour

	// Guessing budget. The password is a word announced in a room, not a
	// passphrase, so online guessing is the threat that matters.
	maxAttempts    = 8
	attemptWindow  = 15 * time.Minute
	lockoutMessage = "Too many tries. Wait a few minutes."
)

// locked returns true when a password is configured for this installation.
func (s *Server) locked(r *http.Request) (bool, string) {
	hash, err := s.store.PasswordHash(r.Context())
	if err != nil {
		// Fail closed. If the password cannot be read, assume there is one.
		s.log.ErrorContext(r.Context(), "read password hash", "error", err)
		return true, ""
	}
	return hash != "", hash
}

// authenticated reports whether the request carries a valid session cookie.
func (s *Server) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	key, err := s.store.SessionKey(r.Context())
	if err != nil {
		s.log.ErrorContext(r.Context(), "read session key", "error", err)
		return false
	}
	return auth.ValidSession(key, cookie.Value, time.Now())
}

// requirepassword wraps the router. When a password is set, anything that is
// not the sign-in page, the sign-in endpoint, a static asset, or the health
// check needs a valid session.
//
// Static assets are deliberately left open: the sign-in page needs the
// stylesheet to render, and no guest data lives in a .css file. Everything
// that touches the roster or a draw goes through /api/.
func (s *Server) requirePassword(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAlwaysOpen(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		lock, _ := s.locked(r)
		if !lock || s.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}

		// An API caller wants a status code it can act on; a browser wants a
		// page it can type a password into.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.writeJSON(w, r, http.StatusUnauthorized,
				errorBody{errorDetail{Code: "unauthorized", Message: "This raffle is password protected."}})
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write(s.login)
	})
}

func isAlwaysOpen(path string) bool {
	switch path {
	case "/api/health", "/api/session", "/styles.css":
		return true
	}
	return false
}

// handleLogin checks the shared password and, if it is right, hands back a
// session cookie.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}

	caller := clientIP(r)
	if !s.attempts.allow(caller) {
		s.writeJSON(w, r, http.StatusTooManyRequests,
			errorBody{errorDetail{Code: "rate_limited", Message: lockoutMessage}})
		return
	}

	hash, err := s.store.PasswordHash(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if hash == "" {
		// Nothing to sign in to.
		s.writeJSON(w, r, http.StatusOK, map[string]any{"authenticated": true, "locked": false})
		return
	}

	ok, err := auth.VerifyPassword(hash, body.Password)
	if err != nil {
		s.log.ErrorContext(r.Context(), "verify password", "error", err)
		s.writeJSON(w, r, http.StatusInternalServerError,
			errorBody{errorDetail{Code: "internal", Message: "Could not check the password."}})
		return
	}
	if !ok {
		s.attempts.fail(caller)
		s.log.WarnContext(r.Context(), "failed sign-in", "ip", caller)
		s.writeJSON(w, r, http.StatusUnauthorized,
			errorBody{errorDetail{Code: "unauthorized", Message: "That is not the password."}})
		return
	}

	key, err := s.store.SessionKey(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.attempts.succeed(caller)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    auth.IssueSession(key, time.Now(), SessionTTL),
		Path:     "/",
		MaxAge:   int(SessionTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Behind Cloudflare the hop to this process is plain HTTP, so trust
		// the forwarded scheme to decide whether the cookie may be sent only
		// over TLS.
		Secure: requestIsHTTPS(r),
	})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"authenticated": true, "locked": true})
}

// handleLogout drops the session cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"authenticated": false})
}

// handleSession lets the page ask where it stands without guessing from a 401.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	lock, _ := s.locked(r)
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"locked":        lock,
		"authenticated": !lock || s.authenticated(r),
	})
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// clientIP identifies the caller for rate limiting. Behind Cloudflare the
// socket address is a Cloudflare edge, so the forwarded header is the only
// thing that distinguishes one guesser from another.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if first, _, found := strings.Cut(fwd, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// attemptLimiter throttles password guessing, per caller.
type attemptLimiter struct {
	mu      sync.Mutex
	entries map[string]*attemptRecord
}

type attemptRecord struct {
	count int
	first time.Time
}

func newAttemptLimiter() *attemptLimiter {
	return &attemptLimiter{entries: map[string]*attemptRecord{}}
}

// allow reports whether this caller may try again.
func (l *attemptLimiter) allow(caller string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.entries[caller]
	if !ok {
		return true
	}
	if time.Since(rec.first) > attemptWindow {
		delete(l.entries, caller)
		return true
	}
	return rec.count < maxAttempts
}

func (l *attemptLimiter) fail(caller string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.entries[caller]
	if !ok || time.Since(rec.first) > attemptWindow {
		l.entries[caller] = &attemptRecord{count: 1, first: time.Now()}
		return
	}
	rec.count++

	// Opportunistic sweep so a long-running server does not accumulate an
	// entry per guesser forever.
	if len(l.entries) > 1024 {
		for k, v := range l.entries {
			if time.Since(v.first) > attemptWindow {
				delete(l.entries, k)
			}
		}
	}
}

func (l *attemptLimiter) succeed(caller string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, caller)
}
