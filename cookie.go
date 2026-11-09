// Session cookie wiring. A session is exported through MUSICFOX_COOKIE,
// and every verb EXCEPT the login lifecycle installs it as the upstream
// library's global jar before running. The login subtree obtains a
// session rather than using one, so it never reads the variable: a mint
// made under the session it replaces is what NetEase refuses with -462.
//
// A credential is SESSION material, not a jar dump. The request-strategy
// cookies go-musicfox injects per call (os, NMTID) are filtered at both
// boundaries — load and publish — so one never reaches a stored
// credential or a reader.

package main

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"

	"github.com/go-musicfox/netease-music/util"
)

const cookieEnvVar = "MUSICFOX_COOKIE"

// requestStrategyCookieNames are the client-identity cookies
// go-musicfox's util.ApplyRequestStrategy injects for ONE request. They
// are not session material: os=pc without the appver that pairs with it
// is the client shape NetEase's QR mint refuses with -462, and NMTID is a
// per-request visitor id. A denylist rather than an allowlist of session
// tokens, so a new upstream session cookie still survives a login;
// upgrade path: keep the strategy out of the session jar entirely (a
// per-request clone) if the library adds more of them.
var requestStrategyCookieNames = map[string]bool{
	"os":    true,
	"NMTID": true,
}

// withoutRequestStrategy answers *cookies* minus the request-strategy
// ones. ONE predicate for both credential boundaries (load and publish):
// a second copy of the name set would drift the first time the library
// adds a cookie.
func withoutRequestStrategy(cookies []*http.Cookie) []*http.Cookie {
	kept := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if requestStrategyCookieNames[cookie.Name] {
			continue
		}
		kept = append(kept, cookie)
	}
	return kept
}

// parseCookieHeader decodes a Cookie-header string ("MUSIC_U=...; __csrf=x")
// into cookies scoped to the netease domain. A pair without "=" is bad
// input, not something to skip: a silently dropped MUSIC_U would read
// as "logged in but every track refused".
func parseCookieHeader(raw string) ([]*http.Cookie, error) {
	var cookies []*http.Cookie
	for _, pair := range strings.Split(raw, ";") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, found := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, fmt.Errorf("%s carries a malformed pair %q (want name=value)", cookieEnvVar, pair)
		}
		cookies = append(cookies, &http.Cookie{
			Name:   name,
			Value:  strings.TrimSpace(value),
			Domain: ".music.163.com",
			Path:   "/",
		})
	}
	if len(cookies) == 0 {
		return nil, fmt.Errorf("%s is set but carries no cookie pairs", cookieEnvVar)
	}
	return cookies, nil
}

// installSession installs the exported MUSICFOX_COOKIE for *verb*.
//
// The login subtree is how a session is OBTAINED, not used: a mint made
// under the session it replaces is refused by NetEase with -462, and
// qq-cli's login verbs read no credential either. Every other verb keeps
// the exported session.
func installSession(verb string) error {
	if verb == "login" {
		return nil
	}
	return applyCookieEnv()
}

// applyCookieEnv installs the MUSICFOX_COOKIE session, if any, as the
// upstream library's global cookie jar. The library then augments the
// jar with its own device-id cookies on first use.
//
// Request-strategy cookies are dropped on the way in: a credential is
// session material, and a persisted os=pc without its appver is the
// exact client shape NetEase's QR mint refuses with -462.
func applyCookieEnv() error {
	raw := strings.TrimSpace(os.Getenv(cookieEnvVar))
	if raw == "" {
		return nil
	}
	cookies, err := parseCookieHeader(raw)
	if err != nil {
		return err
	}
	cookies = withoutRequestStrategy(cookies)
	if len(cookies) == 0 {
		return fmt.Errorf("%s carries no session cookie", cookieEnvVar)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("cannot create cookie jar: %w", err)
	}
	base, err := url.Parse("https://music.163.com")
	if err != nil {
		return fmt.Errorf("cannot parse netease base url: %w", err)
	}
	jar.SetCookies(base, cookies)
	util.SetGlobalCookieJar(jar)
	return nil
}

// renderCookieHeader serialises a jar's cookies back into the Cookie-
// header string MUSICFOX_COOKIE carries.
//
// The exact inverse of parseCookieHeader: what a QR login publishes is
// what this same binary accepts back, so the credential makes one round
// trip through one format rather than sprouting a second one.
func renderCookieHeader(cookies []*http.Cookie) string {
	pairs := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		pairs = append(pairs, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(pairs, "; ")
}

// sessionCookieHeader reads the live session out of the global jar.
//
// Called after an authorised QR poll or a token refresh, both of which
// land their Set-Cookie headers in that jar; an empty result means the
// caller reached here without a session, which is a bug in the flow
// rather than a state to publish.
//
// The jar may also hold request-strategy cookies written by the
// library's CheckQR / LoginRefresh; they are request-scoped and are
// filtered out before publication (see withoutRequestStrategy).
func sessionCookieHeader() (string, error) {
	base, err := url.Parse("https://music.163.com")
	if err != nil {
		return "", fmt.Errorf("cannot parse netease base url: %w", err)
	}
	jar := util.GetGlobalCookieJar()
	if jar == nil {
		return "", fmt.Errorf("no cookie jar after authorisation")
	}
	header := renderCookieHeader(withoutRequestStrategy(jar.Cookies(base)))
	if header == "" {
		return "", fmt.Errorf("authorisation left no session cookies in the jar")
	}
	return header, nil
}

// cookieEnvIsSet reports whether a session was exported at all.
//
// The refresh verb needs the distinction parseCookieHeader does not
// make: "nothing exported" is a caller mistake worth naming, while an
// exported-but-rejected session is upstream's verdict.
func cookieEnvIsSet() bool {
	return strings.TrimSpace(os.Getenv(cookieEnvVar)) != ""
}
