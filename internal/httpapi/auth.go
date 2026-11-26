package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/davepintodev/marquee/internal/config"
)

const (
	sessionCookie = "marquee_session"
	sessionMaxAge = 365 * 24 * 60 * 60
)

// RequireToken answers 401 to every API request that lacks the bearer token.
// An empty token means no auth, for a trusted LAN.
//
// A browser's <img> and EventSource cannot send a header. A request that
// does carry the token is therefore also handed a session cookie, and the
// cookie alone is enough for GET /api/events, GET /api/images/ and a
// person's image, and for nothing else. A query-string token would end up in
// access logs and the browser history; the cookie is HttpOnly and SameSite=Strict.
//
// The file identity lookup is never open: it answers the lookup token or
// the API token, and 401 when neither is configured. The lookup token
// opens nothing else and earns no session.
func RequireToken(token, lookup config.Secret, next http.Handler) http.Handler {
	api := requireAPIToken(token, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != IdentityPath || r.Method != http.MethodGet {
			api.ServeHTTP(w, r)
			return
		}
		if given, ok := bearer(r); ok && (matches(given, lookup) || matches(given, token)) {
			next.ServeHTTP(w, r)
			return
		}
		unauthorized(w)
	})
}

// matches compares in constant time. Hashing first gives
// ConstantTimeCompare equal-length inputs, which is the only way it does
// not leak the token's length; an empty token matches nothing.
func matches(given string, token config.Secret) bool {
	if token == "" {
		return false
	}
	got, want := sha256.Sum256([]byte(given)), sha256.Sum256([]byte(token.Reveal()))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

func bearer(r *http.Request) (string, bool) {
	scheme, given, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return given, ok && strings.EqualFold(scheme, "Bearer")
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	fail(w, http.StatusUnauthorized, "this marquee needs its API token")
}

func requireAPIToken(token config.Secret, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	// The cookie holds a value derived from the token, so the token itself
	// is never written to a response, and changing the token ends every session.
	mac := hmac.New(sha256.New, []byte(token.Reveal()))
	mac.Write([]byte("marquee session"))
	session := hex.EncodeToString(mac.Sum(nil))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasSession := holdsSession(r, session)
		if given, ok := bearer(r); ok {
			if matches(given, token) {
				if !hasSession {
					http.SetCookie(w, &http.Cookie{
						Name: sessionCookie, Value: session, Path: "/api/", MaxAge: sessionMaxAge,
						HttpOnly: true, SameSite: http.SameSiteStrictMode,
					})
				}
				next.ServeHTTP(w, r)
				return
			}
		}
		// SameSite ignores ports, so another app on this host could send the
		// cookie: it must never open anything that changes marquee.
		if hasSession && sessionMayRead(r) {
			next.ServeHTTP(w, r)
			return
		}
		unauthorized(w)
	})
}

func holdsSession(r *http.Request, session string) bool {
	for _, c := range r.Cookies() {
		if c.Name == sessionCookie && hmac.Equal([]byte(c.Value), []byte(session)) {
			return true
		}
	}
	return false
}

func sessionMayRead(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	return p == "/api/events" || strings.HasPrefix(p, "/api/images/") || (strings.HasPrefix(p, "/api/people/") && strings.HasSuffix(p, "/image"))
}
