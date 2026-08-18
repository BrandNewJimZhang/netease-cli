// Session cookie wiring. netease-cli still implements no login flow —
// the login entry lives in the caller (AutoSkill's music panel), which
// exports an existing browser session via MUSICFOX_COOKIE. This file is
// what makes that variable real: parse the Cookie-header string and
// hand it to the upstream library's global jar before any verb runs.

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

// applyCookieEnv installs the MUSICFOX_COOKIE session, if any, as the
// upstream library's global cookie jar. The library then augments the
// jar with its own device-id cookies on first use.
func applyCookieEnv() error {
	raw := strings.TrimSpace(os.Getenv(cookieEnvVar))
	if raw == "" {
		return nil
	}
	cookies, err := parseCookieHeader(raw)
	if err != nil {
		return err
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
