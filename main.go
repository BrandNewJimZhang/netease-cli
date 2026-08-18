// netease-cli — an agent-native resolver for NetEase Cloud Music.
//
// Three read-only verbs (search / url / lyric) over the
// go-musicfox/netease-music library, each answering one JSON envelope
// on stdout. Written for AutoSkill's music app, which drives it as a
// host-existing binary off PATH — the same posture lark-cli has.
//
// Contract (mirrored by the AutoSkill runner's tests):
//   - stdout: {"schema_version":1,"data":<payload>}
//   - stderr: one {"error_class","message"} line on failure
//   - exit 0 ok / 3 bad input / 4 upstream refused
//
// Not implemented on purpose: login flows (an existing session is read
// from MUSICFOX_COOKIE — see cookie.go) and UnblockNeteaseMusic (SkipUNM
// is always true — this tool plays what your account may already play,
// and does not unlock paid content).

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/go-musicfox/netease-music/service"
)

const (
	exitOK             = 0
	exitBadInput       = 3
	exitUpstreamRefuse = 4
)

const usage = `netease-cli — NetEase Cloud Music resolver

usage:
  netease-cli search --keyword <text> [--limit N] [--format json]
  netease-cli url    --id <track-id> [--quality <bitrate>] [--format json]
  netease-cli lyric  --id <track-id> [--format json]

Every verb prints {"schema_version":1,"data":...} on stdout. Failures
print {"error_class","message"} on stderr and exit 3 (bad input) or
4 (upstream refused).`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(exitBadInput)
	}
	if err := applyCookieEnv(); err != nil {
		failInput(err.Error())
		os.Exit(exitBadInput)
	}
	switch os.Args[1] {
	case "search":
		os.Exit(runSearch(os.Args[2:]))
	case "url":
		os.Exit(runURL(os.Args[2:]))
	case "lyric":
		os.Exit(runLyric(os.Args[2:]))
	case "-h", "--help", "help":
		fmt.Println(usage)
		os.Exit(exitOK)
	default:
		failInput(fmt.Sprintf("unknown command %q", os.Args[1]))
		os.Exit(exitBadInput)
	}
}

// emit writes the success envelope. A marshal failure here is a bug in
// our own payload, so it surfaces as an upstream-shaped error rather
// than a silent empty stdout.
func emit(data any) int {
	out, err := json.Marshal(newEnvelope(data))
	if err != nil {
		failUpstream(fmt.Sprintf("cannot encode response: %v", err))
		return exitUpstreamRefuse
	}
	fmt.Println(string(out))
	return exitOK
}

func failWith(class, message string) {
	out, err := json.Marshal(newErrorEnvelope(class, message))
	if err != nil {
		// Last resort: the structured channel itself is broken.
		fmt.Fprintln(os.Stderr, message)
		return
	}
	fmt.Fprintln(os.Stderr, string(out))
}

func failInput(message string)    { failWith("bad_input", message) }
func failUpstream(message string) { failWith("upstream_rejected", message) }

// formatFlag registers the --format flag every verb accepts. JSON is
// the only format: the flag exists so the caller's argv is explicit
// about it, and any other value is rejected rather than ignored.
func formatFlag(fs *flag.FlagSet) *string {
	return fs.String("format", "json", "output format (json only)")
}

func checkFormat(format string) error {
	if format != "json" {
		return fmt.Errorf("unsupported --format %q; this CLI emits json only", format)
	}
	return nil
}

func runSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	keyword := fs.String("keyword", "", "search text (required)")
	limit := fs.Int("limit", 20, "maximum rows")
	format := formatFlag(fs)
	if err := fs.Parse(args); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if err := checkFormat(*format); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if *keyword == "" {
		failInput("--keyword is required")
		return exitBadInput
	}
	if *limit <= 0 {
		failInput("--limit must be positive")
		return exitBadInput
	}

	svc := &service.SearchService{
		S:     *keyword,
		Type:  "1", // songs
		Limit: fmt.Sprintf("%d", *limit),
	}
	code, body := svc.Search()
	if code != 200 {
		failUpstream(fmt.Sprintf("search rejected with code %v", code))
		return exitUpstreamRefuse
	}
	tracks, err := mapSearchResponse(body)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	return emit(tracks)
}

func runURL(args []string) int {
	fs := flag.NewFlagSet("url", flag.ContinueOnError)
	id := fs.String("id", "", "track id (required)")
	quality := fs.String("quality", "320000", "bitrate in bps")
	format := formatFlag(fs)
	if err := fs.Parse(args); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if err := checkFormat(*format); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if *id == "" {
		failInput("--id is required")
		return exitBadInput
	}

	// SkipUNM stays true: unlocking paid content is out of scope.
	svc := &service.SongUrlService{ID: *id, Br: *quality, SkipUNM: true}
	code, body := svc.SongUrl()
	if code != 200 {
		failUpstream(fmt.Sprintf("url rejected with code %v", code))
		return exitUpstreamRefuse
	}
	resolved, err := mapUrlResponse(body)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	return emit(resolved)
}

func runLyric(args []string) int {
	fs := flag.NewFlagSet("lyric", flag.ContinueOnError)
	id := fs.String("id", "", "track id (required)")
	format := formatFlag(fs)
	if err := fs.Parse(args); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if err := checkFormat(*format); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if *id == "" {
		failInput("--id is required")
		return exitBadInput
	}

	svc := &service.LyricService{ID: *id}
	code, body := svc.Lyric()
	if code != 200 {
		failUpstream(fmt.Sprintf("lyric rejected with code %v", code))
		return exitUpstreamRefuse
	}
	lrc, err := mapLyricResponse(body)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	return emit(Lyric{ID: *id, LRC: lrc})
}
