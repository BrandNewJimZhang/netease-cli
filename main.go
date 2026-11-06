// netease-cli — an agent-native resolver for NetEase Cloud Music.
//
// Read-only resolution (search / url / lyric / whoami) and the account's
// own library (playlists / playlist / daily), plus the login lifecycle
// the session needs (login start / login poll / refresh), over
// the go-musicfox/netease-music library, each answering one JSON
// envelope on stdout. Written for AutoSkill's music app, which drives it
// as a host-existing binary off PATH — the same posture lark-cli has.
//
// Contract (mirrored by the AutoSkill runner's tests):
//   - stdout: {"schema_version":2,"data":<payload>}
//   - stderr: one {"error_class","message","detail"} line on failure
//   - exit 0 ok / 3 bad input / 4 upstream refused
//
// A session is read from MUSICFOX_COOKIE (see cookie.go) and is exactly
// what `login poll` publishes on success — one format, one round trip.
//
// Not implemented on purpose: UnblockNeteaseMusic (SkipUNM is always
// true — this tool plays what your account may already play, and does
// not unlock paid content).

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-musicfox/netease-music/service"
	"github.com/go-musicfox/netease-music/util"
)

const (
	exitOK             = 0
	exitBadInput       = 3
	exitUpstreamRefuse = 4
)

const usage = `netease-cli — NetEase Cloud Music resolver

usage:
  netease-cli search --keyword <text> [--limit N] [--format json]
  netease-cli url    --id <track-id> [--quality lossless|high|standard] [--format json]
  netease-cli lyric  --id <track-id> [--format json]
  netease-cli whoami [--format json]

  netease-cli playlists [--limit N] [--format json]
  netease-cli playlist  --id <playlist-id> [--limit N] [--format json]
  netease-cli daily [--format json]

  netease-cli login start [--format json]
  netease-cli login poll  --identifier <token> [--format json]
  netease-cli refresh [--format json]

Every verb prints {"schema_version":2,"data":...} on stdout. Failures
print ONE {"error_class","message","detail"} line on stderr and exit 3
(bad input) or 4 (upstream refused). "message" is the sentence a reader
may see; "detail" is the raw upstream body, capped at 300 runes.`

// silenceUpstreamLogger points the standard logger at io.Discard.
//
// The upstream library dumps full request internals — url, method,
// encrypted params, response body, cookies — through “log.Printf“ on
// ANY non-200. Two things break if that reaches stderr: this CLI's
// contract reserves stderr for ONE error-envelope line, and a caller
// that parses it as JSON loses the error CLASS to the prepended text;
// and the dump puts a live session's cookies into whatever collects
// logs. Neither is something a caller can opt out of, so the writer is
// closed here rather than left to the operator.
func silenceUpstreamLogger() {
	log.SetOutput(io.Discard)
}

func main() {
	silenceUpstreamLogger()
	if len(os.Args) < 2 {
		// One envelope line, like every other failure: a caller parsing
		// stderr must never have to special-case the no-argument path.
		// The message is a sentence, not the usage block (which --help
		// prints to stdout).
		failInput("a command is required; pass --help for usage")
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
	case "whoami":
		os.Exit(runWhoami(os.Args[2:]))
	case "playlists":
		os.Exit(runPlaylists(os.Args[2:]))
	case "playlist":
		os.Exit(runPlaylist(os.Args[2:]))
	case "daily":
		os.Exit(runDaily(os.Args[2:]))
	case "refresh":
		os.Exit(runRefresh(os.Args[2:]))
	case "login":
		if len(os.Args) < 3 {
			failInput("login needs a subcommand: start or poll")
			os.Exit(exitBadInput)
		}
		switch os.Args[2] {
		case "start":
			os.Exit(runLoginStart(os.Args[3:]))
		case "poll":
			os.Exit(runLoginPoll(os.Args[3:]))
		default:
			failInput(fmt.Sprintf("unknown login subcommand %q; want start or poll", os.Args[2]))
			os.Exit(exitBadInput)
		}
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

func failWith(class, message, detail string) {
	out, err := json.Marshal(newErrorEnvelope(class, message, detail))
	if err != nil {
		// Last resort: the structured channel itself is broken.
		fmt.Fprintln(os.Stderr, message)
		return
	}
	fmt.Fprintln(os.Stderr, string(out))
}

func failInput(message string) { failWith("bad_input", message, "") }
func failUpstream(message string) {
	failWith("upstream_rejected", message, "")
}
func failUpstreamDetail(message, detail string) {
	failWith("upstream_rejected", message, detail)
}

// newFlagSet is the ONE constructor for a verb's parser. Its output is
// discarded so a parse failure is reported ONLY through our envelope:
// the flag package would otherwise print the error and the usage text
// first, and a caller reading stderr as JSON would lose the class.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

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
	fs := newFlagSet("search")
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
		message, detail := rejected("search", code, body)
		failUpstreamDetail(message, detail)
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
	fs := newFlagSet("url")
	id := fs.String("id", "", "track id (required)")
	quality := fs.String("quality", "", "tier: lossless / high / standard")
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

	ladder := ladderFrom(*quality)
	if ladder == nil {
		failInput(fmt.Sprintf(
			"unknown --quality %q; want one of %s", *quality,
			strings.Join(qualityTiers, " / ")))
		return exitBadInput
	}

	// Walk down from the requested tier and answer the first rung that
	// yields a stream. Upstream refuses a rung the account may not play
	// (null url), which is a probe miss here — the refusal is reported
	// only after the LAST rung, so the message names the whole attempt
	// rather than one arbitrary rung of it.
	var lastErr error
	for _, tier := range ladder {
		// SkipUNM stays true: unlocking paid content is out of scope.
		svc := &service.SongUrlService{
			ID:      *id,
			Br:      strconv.FormatInt(tierBitrate(tier), 10),
			SkipUNM: true,
		}
		code, body := svc.SongUrl()
		if code != 200 {
			message, detail := rejected("url", code, body)
			failUpstreamDetail(message, detail)
			return exitUpstreamRefuse
		}
		resolved, err := mapUrlResponse(body, tier)
		if err != nil {
			lastErr = err
			continue
		}
		return emit(resolved)
	}
	failUpstream(fmt.Sprintf(
		"no playable stream for track %s at %s or below: %v",
		*id, ladder[0], lastErr))
	return exitUpstreamRefuse
}

func runWhoami(args []string) int {
	fs := newFlagSet("whoami")
	format := formatFlag(fs)
	if err := fs.Parse(args); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if err := checkFormat(*format); err != nil {
		failInput(err.Error())
		return exitBadInput
	}

	svc := &service.UserAccountService{}
	code, body := svc.AccountInfo()
	if code != 200 {
		message, detail := rejected("account", code, body)
		failUpstreamDetail(message, detail)
		return exitUpstreamRefuse
	}
	session, err := mapAccountResponse(body)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	return emit(session)
}

func runLyric(args []string) int {
	fs := newFlagSet("lyric")
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

	code, body := requestLyric(*id)
	if code != 200 {
		message, detail := rejected("lyric", code, body)
		failUpstreamDetail(message, detail)
		return exitUpstreamRefuse
	}
	lyric, err := mapLyricResponse(body)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	lyric.ID = *id
	return emit(lyric)
}

// requestLyric asks the v1 lyric endpoint for one track: the LRC, the
// word-timed yrc, and both timelines' translation and romaji in one
// round trip. Spelled here rather than through the
// library's LyricService because that service is fixed to the older
// /api/song/lyric, which never answers yrc, and takes no parameters to
// ask for it. The field set is the desktop client's; "0" on each asks
// for that document from its first version.
func requestLyric(id string) (float64, []byte) {
	options := &util.Options{
		Crypto:  "linuxapi",
		Cookies: []*http.Cookie{{Name: "os", Value: "pc"}},
	}
	data := map[string]string{
		"id": id, "cp": "false",
		"lv": "0", "tv": "0", "rv": "0", "kv": "0",
		"yv": "0", "ytv": "0", "yrv": "0",
	}
	code, body, _ := util.CreateRequest(
		"POST", "https://music.163.com/api/song/lyric/v1", data, options,
	)
	return code, body
}
