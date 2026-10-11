// Package admin serves authenticated, read-only operations views.
package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
)

type Config struct{ ViewerToken string }

func (c Config) Enabled() bool { return c.ViewerToken != "" }
func (c Config) Validate() error {
	if !c.Enabled() {
		return nil
	}
	if len(c.ViewerToken) < 32 || len(c.ViewerToken) > 256 {
		return errors.New("Invalid ADMIN_VIEWER_TOKEN.")
	}
	for i := 0; i < len(c.ViewerToken); i++ {
		if c.ViewerToken[i] < 0x21 || c.ViewerToken[i] > 0x7e {
			return errors.New("Invalid ADMIN_VIEWER_TOKEN.")
		}
	}
	return nil
}
func (h *handler) authenticated(r *http.Request) bool {
	username, token, ok := r.BasicAuth()
	userDigest, tokenDigest := sha256.Sum256([]byte(username)), sha256.Sum256([]byte(token))
	userMatch := subtle.ConstantTimeCompare(userDigest[:], h.usernameDigest[:])
	tokenMatch := subtle.ConstantTimeCompare(tokenDigest[:], h.tokenDigest[:])
	return ok && userMatch&tokenMatch == 1
}
