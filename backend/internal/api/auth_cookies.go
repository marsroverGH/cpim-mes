package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/service"
)

const (
	productionSessionCookie  = "__Host-cpim-session"
	productionCSRFCookie     = "__Host-cpim-csrf"
	developmentSessionCookie = "cpim-session"
	developmentCSRFCookie    = "cpim-csrf"
)

type authCookieConfig struct {
	secure   bool
	sameSite http.SameSite
}

func newAuthCookieConfig(secure bool, sameSite string) authCookieConfig {
	mode := http.SameSiteLaxMode
	switch strings.ToLower(strings.TrimSpace(sameSite)) {
	case "strict":
		mode = http.SameSiteStrictMode
	case "none":
		mode = http.SameSiteNoneMode
	}
	return authCookieConfig{secure: secure, sameSite: mode}
}

func (c authCookieConfig) sessionName() string {
	if c.secure {
		return productionSessionCookie
	}
	return developmentSessionCookie
}

func (c authCookieConfig) csrfName() string {
	if c.secure {
		return productionCSRFCookie
	}
	return developmentCSRFCookie
}

func (c authCookieConfig) setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.sessionName(),
		Value:    token,
		Path:     "/",
		MaxAge:   int(service.SessionTTL.Seconds()),
		Expires:  time.Now().Add(service.SessionTTL),
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: c.sameSite,
	})
}

func (c authCookieConfig) setCSRF(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.csrfName(),
		Value:    token,
		Path:     "/",
		MaxAge:   int(service.SessionTTL.Seconds()),
		Expires:  time.Now().Add(service.SessionTTL),
		HttpOnly: false,
		Secure:   c.secure,
		SameSite: c.sameSite,
	})
}

func (c authCookieConfig) clear(w http.ResponseWriter) {
	expired := time.Unix(1, 0).UTC()
	for _, spec := range []struct {
		name     string
		httpOnly bool
	}{
		{c.sessionName(), true},
		{c.csrfName(), false},
	} {
		http.SetCookie(w, &http.Cookie{
			Name:     spec.name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			Expires:  expired,
			HttpOnly: spec.httpOnly,
			Secure:   c.secure,
			SameSite: c.sameSite,
		})
	}
}

func sameRequestOrigin(r *http.Request, origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	scheme := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return strings.EqualFold(parsed.Scheme, scheme) && strings.EqualFold(parsed.Host, r.Host)
}

func trustedBrowserOrigin(r *http.Request, allowedOrigins []string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if sameRequestOrigin(r, origin) {
		return true
	}
	for _, allowed := range allowedOrigins {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(allowed), "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	return false
}
