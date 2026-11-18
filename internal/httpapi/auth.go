package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/davepintodev/marquee/internal/config"
)

// RequireToken answers 401 to every API request that lacks the bearer token.
// An empty token means no auth, for a trusted LAN.
//
// Images and the event stream are left open: the browser loads them through
// <img> and EventSource, which cannot send a header, so guarding them would
// blank every poster as soon as a token is set.
func RequireToken(token config.Secret, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	// Hashing first gives ConstantTimeCompare equal-length inputs, which is
	// the only way it does not leak the token's length.
	want := sha256.Sum256([]byte(token.Reveal()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exemptFromToken(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		scheme, given, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if ok && strings.EqualFold(scheme, "Bearer") {
			got := sha256.Sum256([]byte(given))
			if subtle.ConstantTimeCompare(got[:], want[:]) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		fail(w, http.StatusUnauthorized, "this marquee needs its API token")
	})
}

func exemptFromToken(path string) bool {
	return path == "/api/events" || strings.HasPrefix(path, "/api/images/")
}
