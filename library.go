// The account's own library: its playlists, one playlist's tracks, and
// today's recommendations.
//
// All three publish rows the panel already knows — playlist tracks and
// daily songs are the SAME row `search` emits, so a shelf is playable
// and queueable without a second mapper on either side of the wire.
// Only the shelf itself is a new shape, and qq-cli publishes it field
// for field.

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strconv"

	"github.com/go-musicfox/netease-music/service"
)

// currentUserID reads the signed-in account's numeric id.
//
// The shelf endpoint is keyed by uid rather than by the session, so the
// session has to name itself first. An anonymous session cannot: that
// is a named refusal, because "you have no playlists" and "we do not
// know who you are" must not look the same.
func currentUserID() (string, error) {
	svc := &service.UserAccountService{}
	code, body := svc.AccountInfo()
	if code != 200 {
		return "", fmt.Errorf("%s", rejected("account", code, body))
	}
	var parsed struct {
		Profile *struct {
			UserID int64 `json:"userId"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("account response is not valid JSON: %w", err)
	}
	if parsed.Profile == nil || parsed.Profile.UserID == 0 {
		return "", errAnonymous
	}
	return strconv.FormatInt(parsed.Profile.UserID, 10), nil
}

// errAnonymous marks "no signed-in account" so the verbs below report
// it with the class a caller acts on (sign in) rather than as an
// upstream failure (retry).
var errAnonymous = errors.New("this verb is account-scoped; sign in with 'login start' first")

// runPlaylists publishes the account's own shelf.
func runPlaylists(args []string) int {
	fs := flag.NewFlagSet("playlists", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "maximum shelf entries")
	format := formatFlag(fs)
	if err := fs.Parse(args); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if err := checkFormat(*format); err != nil {
		failInput(err.Error())
		return exitBadInput
	}
	if *limit <= 0 {
		failInput("--limit must be positive")
		return exitBadInput
	}
	uid, err := currentUserID()
	if errors.Is(err, errAnonymous) {
		failWith("credential_missing", err.Error())
		return exitBadInput
	}
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	svc := &service.UserPlaylistService{Uid: uid, Limit: strconv.Itoa(*limit)}
	code, body := svc.UserPlaylist()
	if code != 200 {
		failUpstream(rejected("playlist shelf", code, body))
		return exitUpstreamRefuse
	}
	lists, err := mapUserPlaylistResponse(body)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	return emit(lists)
}

// runPlaylist publishes one playlist's tracks.
func runPlaylist(args []string) int {
	fs := flag.NewFlagSet("playlist", flag.ContinueOnError)
	id := fs.String("id", "", "playlist id (required)")
	// Accepted for argv parity with qq-cli, whose upstream pages its
	// detail call. NetEase's assembles the whole shelf in one go — there
	// is no page to ask for — so the cap is applied to the published
	// rows instead of to the request.
	limit := fs.Int("limit", 50, "maximum rows")
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
	if *limit <= 0 {
		failInput("--limit must be positive")
		return exitBadInput
	}
	svc := &service.PlaylistTrackAllService{Id: *id}
	code, body := svc.AllTracks()
	if code != 200 {
		failUpstream(rejected(fmt.Sprintf("playlist %s", *id), code, body))
		return exitUpstreamRefuse
	}
	tracks, err := mapPlaylistTracksResponse(body)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	if len(tracks) > *limit {
		tracks = tracks[:*limit]
	}
	return emit(tracks)
}

// runDaily publishes today's recommended tracks.
func runDaily(args []string) int {
	fs := flag.NewFlagSet("daily", flag.ContinueOnError)
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
		failWith("credential_missing", errAnonymous.Error())
		return exitBadInput
	}
	svc := &service.RecommendSongsService{}
	code, body := svc.RecommendSongs()
	if code != 200 {
		failUpstream(rejected("daily recommendations", code, body))
		return exitUpstreamRefuse
	}
	tracks, err := mapDailySongsResponse(body)
	if err != nil {
		failUpstream(err.Error())
		return exitUpstreamRefuse
	}
	return emit(tracks)
}
