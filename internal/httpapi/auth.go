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
func RequireToken(token config.Secret, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	// Hashing first gives ConstantTimeCompare equal-length inputs, which is
	// the only way it does not leak the token's length.
	want := sha256.Sum256([]byte(token.Reveal()))
	// The cookie holds a value derived from the token, so the token itself
	// is never written to a response, and changing the token ends every session.
	mac := hmac.New(sha256.New, []byte(token.Reveal()))
	mac.Write([]byte("marquee session"))
	session := hex.EncodeToString(mac.Sum(nil))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasSession := holdsSession(r, session)
		scheme, given, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if ok && strings.EqualFold(scheme, "Bearer") {
			got := sha256.Sum256([]byte(given))
			if subtle.ConstantTimeCompare(got[:], want[:]) == 1 {
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
		w.Header().Set("WWW-Authenticate", "Bearer")
		fail(w, http.StatusUnauthorized, "this marquee needs its API token")
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
