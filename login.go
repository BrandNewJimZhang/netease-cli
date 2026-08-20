// QR login and token refresh — the credential lifecycle behind every
// other verb.
//
// Upstream hands out a login URL and a unikey; this file turns that into
// the same two-verb shape qq-cli publishes: `login start` mints a code
// and publishes the image plus the token to poll it by, `login poll`
// checks that token once. The unikey is the whole of the poll state, so
// the two halves run as separate processes and the caller keeps its own
// event loop.
//
// The published credential is a Cookie-header string — exactly what this
// binary accepts back through MUSICFOX_COOKIE, so a login makes one
// round trip through one format.

package main

import (
	"encoding/base64"
	"flag"
	"fmt"

	"github.com/go-musicfox/netease-music/service"
	qrcode "github.com/skip2/go-qrcode"
)

// qrImagePixels is the rendered code's edge length. 256 is the smallest
// size the NetEase app scans reliably off a laptop panel at arm's
// length; the panel scales it down for layout, never up.
const qrImagePixels = 256

// LoginQR is a minted code: the image to render, the token to poll by.
// Field for field what qq-cli publishes, so the panel renders either
// resolver's login without branching.
type LoginQR struct {
	Identifier  string `json:"identifier"`
	LoginType   string `json:"login_type"`
	MimeType    string `json:"mimetype"`
	ImageBase64 string `json:"image_base64"`
}

// LoginStatus is one poll result: the state, and — only once the scan is
// authorised — the credential to store.
type LoginStatus struct {
	State      string      `json:"state"`
	Credential *Credential `json:"credential"`
}

// Credential is a storable session. NetEase's is a single Cookie-header
// string; qq-cli's blob carries more fields. Both are JSON objects whose
// shape the owning resolver decides, which is what lets one store hold
// either.
type Credential struct {
	Cookie string `json:"cookie"`
}

// encodeQRImage renders a login URL as a base64 PNG.
//
// The resolver encodes rather than publishing the bare URL because the
// panel must render one thing for either resolver: qq-cli's upstream
// hands back image bytes, this one hands back a link, and the difference
// stops here rather than becoming a branch in every caller.
func encodeQRImage(loginURL string) (string, error) {
	// Medium recovery is the level NetEase's own client uses; higher
	// levels enlarge the code without helping a screen-to-camera scan.
	png, err := qrcode.Encode(loginURL, qrcode.Medium, qrImagePixels)
	if err != nil {
		return "", fmt.Errorf("cannot render the login qr code: %w", err)
	}
	return base64.StdEncoding.EncodeToString(png), nil
}

// runLoginStart mints a login code.
func runLoginStart(args []string) int {
	fs := flag.NewFlagSet("login start", flag.ContinueOnError)
	format := formatFlag(fs)
	if err := fs.Parse(args); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if err := checkFormat(*format); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	qr := &service.LoginQRService{}
	code, body, loginURL, err := qr.GetKey()
	if err != nil {
		failUpstream(fmt.Sprintf("cannot mint a login qr code: %v", err))
		return exitUpstreamRefuse
	}
	if code != 200 || qr.UniKey == "" {
		failUpstream(rejected("login qr mint", code, body))
		return exitUpstreamRefuse
	}
	image, err := encodeQRImage(loginURL)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	return emit(LoginQR{
		Identifier:  qr.UniKey,
		LoginType:   "netease",
		MimeType:    "image/png",
		ImageBase64: image,
	})
}

// runLoginPoll checks one minted code, publishing the credential once
// the scan is authorised.
func runLoginPoll(args []string) int {
	fs := flag.NewFlagSet("login poll", flag.ContinueOnError)
	identifier := fs.String("identifier", "", "the token 'login start' published")
	format := formatFlag(fs)
	if err := fs.Parse(args); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if err := checkFormat(*format); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if *identifier == "" {
		failInput("--identifier is required")
		return exitBadInput
	}
	// Rebuilt from the identifier alone: that is all upstream's status
	// check reads, which is what lets start and poll be separate runs.
	qr := &service.LoginQRService{UniKey: *identifier}
	_, body, err := qr.CheckQR()
	if err != nil {
		failUpstream(fmt.Sprintf("cannot check the login qr code: %v", err))
		return exitUpstreamRefuse
	}
	state, err := mapQRStatusResponse(body)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	if state != "done" {
		return emit(LoginStatus{State: state})
	}
	// 803 landed the session in the global jar; publishing it is what
	// turns a scan into a credential the caller can store.
	cookie, err := sessionCookieHeader()
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	return emit(LoginStatus{State: state, Credential: &Credential{Cookie: cookie}})
}

// runRefresh extends the exported session.
//
// NetEase renews a session in place: the refresh call answers with fresh
// Set-Cookie headers rather than a new document, so the published
// credential is read back out of the jar the same way a login's is.
func runRefresh(args []string) int {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	format := formatFlag(fs)
	if err := fs.Parse(args); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if err := checkFormat(*format); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if !cookieEnvIsSet() {
		failWith("credential_missing", fmt.Sprintf("nothing to refresh: %s is not set", cookieEnvVar))
		return exitBadInput
	}
	refresh := &service.LoginRefreshService{}
	code, body, err := refresh.LoginRefresh()
	if err != nil {
		failUpstream(fmt.Sprintf("cannot refresh the session: %v", err))
		return exitUpstreamRefuse
	}
	if code != 200 {
		// 301 is upstream's "this session is past renewal" — reported by
		// its own class so a caller does not retry a login that can only
		// be replaced by signing in again.
		failWith("credential_expired", fmt.Sprintf(
			"NetEase refused to renew this session (code %v): %s", code, string(body)))
		return exitUpstreamRefuse
	}
	cookie, err := sessionCookieHeader()
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	return emit(Credential{Cookie: cookie})
}
