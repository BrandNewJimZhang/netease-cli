// Response mappers: upstream NetEase JSON -> this CLI's stable schema.
//
// Kept separate from main.go so every shape decision is unit-testable
// against captured fixtures without touching the network. The upstream
// bodies are wide and unstable; the structs below name exactly the
// fields this CLI publishes and ignore the rest.

package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SchemaVersion is the contract version the AutoSkill runner unwraps.
// Bump when a published field changes meaning or disappears.
const SchemaVersion = 2

// Track is one search result row.
type Track struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Cover    string `json:"cover"`    // album art URL, "" when upstream has none
	Duration int64  `json:"duration"` // milliseconds, as upstream reports
}

// PlayableURL is one resolved stream.
//
// Both halves of the quality answer ride the wire: “Quality“ is the
// TIER that answered (the word the panel offers), “Bitrate“ is what
// actually came back in kbps. Publishing only the tier would hide a
// 320k stream answered under a lossless request; publishing only the
// bitrate would hand the panel a number it cannot offer as a choice.
type PlayableURL struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Quality string `json:"quality"`
	Bitrate int64  `json:"bitrate"` // kbps, 0 when upstream does not say
}

// Playlist is one shelf entry. Field for field what qq-cli publishes,
// so the panel renders either resolver's shelf without branching.
type Playlist struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Cover       string `json:"cover"` // shelf art URL, "" when upstream has none
	Count       int64  `json:"count"` // tracks in the playlist
	Description string `json:"description"`
}

// Lyric is one track's LRC document.
type Lyric struct {
	ID  string `json:"id"`
	LRC string `json:"lrc"`
}

// Session is the whoami verdict: who the exported cookie authenticates
// as. Anonymous and rejected cookies both read LoggedIn false — that is
// the verdict a caller's login verification needs.
type Session struct {
	LoggedIn bool   `json:"logged_in"`
	Nickname string `json:"nickname"`
	VIP      bool   `json:"vip"`
}

type envelope struct {
	SchemaVersion int `json:"schema_version"`
	Data          any `json:"data"`
}

type errorEnvelope struct {
	ErrorClass string `json:"error_class"`
	Message    string `json:"message"`
}

func newEnvelope(data any) envelope {
	return envelope{SchemaVersion: SchemaVersion, Data: data}
}

func newErrorEnvelope(class, message string) errorEnvelope {
	return errorEnvelope{ErrorClass: class, Message: message}
}

// upstreamSong is the per-song object NetEase repeats across every
// endpoint that returns music: cloudsearch nests it under
// result.songs, the playlist endpoint answers a bare array of it, and
// the daily recommendation nests it under data.dailySongs. Declared
// once so all three decode through the same field names — three copies
// would be one drift away from three different published row shapes.
type upstreamSong struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Dt   int64  `json:"dt"`
	Ar   []struct {
		Name string `json:"name"`
	} `json:"ar"`
	Al struct {
		Name   string `json:"name"`
		PicURL string `json:"picUrl"`
	} `json:"al"`
}

// publishTracks turns decoded upstream songs into published rows.
func publishTracks(songs []upstreamSong) []Track {
	tracks := make([]Track, 0, len(songs))
	for _, song := range songs {
		names := make([]string, 0, len(song.Ar))
		for _, artist := range song.Ar {
			if artist.Name != "" {
				names = append(names, artist.Name)
			}
		}
		tracks = append(tracks, Track{
			ID:       strconv.FormatInt(song.ID, 10),
			Title:    song.Name,
			Artist:   strings.Join(names, " / "),
			Album:    song.Al.Name,
			Cover:    song.Al.PicURL,
			Duration: song.Dt,
		})
	}
	return tracks
}

// mapSearchResponse decodes a cloudsearch body into published rows.
//
// An empty song list is a valid empty result; a body that does not
// parse is corrupt and raises (fail-fast: empty and corrupt are
// different states and must not collapse).
func mapSearchResponse(body []byte) ([]Track, error) {
	var parsed struct {
		Result struct {
			Songs []upstreamSong `json:"songs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("search response is not valid JSON: %w", err)
	}
	return publishTracks(parsed.Result.Songs), nil
}

// mapPlaylistTracksResponse decodes one playlist's tracks.
//
// Upstream's detail body carries only track IDs; the library fetches the
// song details separately and writes them BACK into the same body under
// playlist.tracks, so that is where the rows are — not in a bare array,
// and not under the `songs` key search uses.
func mapPlaylistTracksResponse(body []byte) ([]Track, error) {
	var parsed struct {
		Playlist struct {
			Tracks []upstreamSong `json:"tracks"`
		} `json:"playlist"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("playlist track response is not valid JSON: %w", err)
	}
	return publishTracks(parsed.Playlist.Tracks), nil
}

// mapDailySongsResponse decodes the daily recommendation body.
//
// An empty day is a valid empty state — a new account, or upstream not
// having computed today's set yet — so it publishes as no rows rather
// than raising.
func mapDailySongsResponse(body []byte) ([]Track, error) {
	var parsed struct {
		Data struct {
			DailySongs []upstreamSong `json:"dailySongs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("daily songs response is not valid JSON: %w", err)
	}
	return publishTracks(parsed.Data.DailySongs), nil
}

// mapUserPlaylistResponse decodes the account's own shelf.
//
// The playlist row is the second shape both resolvers publish (after
// the track row) — qq-cli emits these exact field names, so one panel
// renders either shelf.
func mapUserPlaylistResponse(body []byte) ([]Playlist, error) {
	var parsed struct {
		Playlist []struct {
			ID          int64   `json:"id"`
			Name        string  `json:"name"`
			CoverImgURL string  `json:"coverImgUrl"`
			TrackCount  int64   `json:"trackCount"`
			Description *string `json:"description"`
		} `json:"playlist"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("user playlist response is not valid JSON: %w", err)
	}
	lists := make([]Playlist, 0, len(parsed.Playlist))
	for _, entry := range parsed.Playlist {
		description := ""
		if entry.Description != nil {
			description = *entry.Description
		}
		lists = append(lists, Playlist{
			ID:          strconv.FormatInt(entry.ID, 10),
			Title:       entry.Name,
			Cover:       entry.CoverImgURL,
			Count:       entry.TrackCount,
			Description: description,
		})
	}
	return lists, nil
}

// mapAccountResponse decodes /api/nuser/account/get into a Session.
//
// A signed-in session carries a profile object; anonymous (or a cookie
// the server rejected) answers profile null under the same 200 — so
// null profile IS the "not signed in" verdict, not an error. A body
// that does not parse is corrupt and raises.
func mapAccountResponse(body []byte) (Session, error) {
	var parsed struct {
		Profile *struct {
			Nickname string `json:"nickname"`
			VipType  int64  `json:"vipType"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Session{}, fmt.Errorf("account response is not valid JSON: %w", err)
	}
	if parsed.Profile == nil {
		return Session{}, nil
	}
	return Session{
		LoggedIn: true,
		Nickname: parsed.Profile.Nickname,
		VIP:      parsed.Profile.VipType > 0,
	}, nil
}

// qualityTiers is the shared vocabulary, best first. A caller asks for
// a TIER, never a bitrate: qq-cli's upstream exposes file types with no
// bitrate knob at all, so bps could never have been the shared word.
// One list, two resolvers, ordered so it doubles as the fallback ladder.
var qualityTiers = []string{"lossless", "high", "standard"}

// tierBitrate maps a tier to what NetEase's bitrate parameter wants.
// Above-lossless values are pointless here: upstream caps at what the
// account may play and answers the best it can below the ask.
func tierBitrate(tier string) int64 {
	switch tier {
	case "lossless":
		return 999000
	case "high":
		return 320000
	case "standard":
		return 128000
	}
	return 0
}

// ladderFrom returns the tiers to probe for a requested tier, best
// first, starting AT the request.
//
// Starting at the request rather than the top is the point: a caller
// that picked the cheap tier gets the cheap tier, and only the fallback
// direction (down) is automatic. An unknown tier yields no ladder — the
// verb reports it rather than silently substituting a default.
func ladderFrom(requested string) []string {
	if requested == "" {
		return qualityTiers
	}
	for i, tier := range qualityTiers {
		if tier == requested {
			return qualityTiers[i:]
		}
	}
	return nil
}

// mapUrlResponse decodes a player-url body into one playable stream.
//
// Upstream answers 200 with a null url for a paid or region-locked
// track. That is a refusal, not a result: publishing an empty url would
// hand the player nothing and read as a bug on our side, so it raises.
func mapUrlResponse(body []byte, tier string) (PlayableURL, error) {
	var parsed struct {
		Data []struct {
			ID  int64   `json:"id"`
			URL *string `json:"url"`
			Br  int64   `json:"br"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return PlayableURL{}, fmt.Errorf("url response is not valid JSON: %w", err)
	}
	if len(parsed.Data) == 0 {
		return PlayableURL{}, fmt.Errorf("url response carries no entry")
	}
	entry := parsed.Data[0]
	if entry.URL == nil || *entry.URL == "" {
		return PlayableURL{}, fmt.Errorf(
			"upstream returned no playable url for track %d (paid, region-locked, or requires login)",
			entry.ID,
		)
	}
	return PlayableURL{
		ID:      strconv.FormatInt(entry.ID, 10),
		URL:     *entry.URL,
		Quality: tier,
		Bitrate: entry.Br / 1000,
	}, nil
}

// mapLyricResponse decodes a lyric body into its LRC document.
//
// A track with no lyric returns "" rather than raising: an instrumental
// is a normal state, and the panel renders an empty lyric column.
func mapLyricResponse(body []byte) (string, error) {
	var parsed struct {
		Lrc struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("lyric response is not valid JSON: %w", err)
	}
	return parsed.Lrc.Lyric, nil
}

// qrStates maps NetEase's qrcode/client/login status code to the state
// the caller branches on. The names match qq-cli's — one schema, two
// resolvers, one panel that need not know which answered.
//
// 801/802 mean "keep polling"; 800 means the code is dead and a new one
// must be minted. NetEase has no distinct "user refused" code (a
// refusal simply lets the code expire), so `refused` is a state this
// resolver never publishes while qq-cli does — the caller handles both
// the same way regardless.
var qrStates = map[int64]string{
	800: "expired",
	801: "pending",
	802: "scanned",
	803: "done",
}

// mapQRStatusResponse decodes one poll body into its published state.
//
// A code this mapper does not know raises rather than defaulting to
// "pending": a caller told to keep polling a code upstream has stopped
// honouring would spin until its own timeout with nothing to show.
func mapQRStatusResponse(body []byte) (string, error) {
	var parsed struct {
		Code int64 `json:"code"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("qr status response is not valid JSON: %w", err)
	}
	state, known := qrStates[parsed.Code]
	if !known {
		return "", fmt.Errorf("unknown qr status code %d", parsed.Code)
	}
	return state, nil
}

// rejected names an upstream refusal, carrying the detail the library
// put in the BODY. Transport failures never reach a status line: the
// library reports them as sentinel code 520 whose body is the
// underlying error text (dns timeout, dial refused, tls reset), so a
// message without the body turns an actionable failure into a bare
// number. Real HTTP error bodies can be whole pages — the detail is
// truncated to one readable line, rune-safely.
func rejected(what string, code float64, body []byte) string {
	detail := strings.TrimSpace(string(body))
	if runes := []rune(detail); len(runes) > 300 {
		detail = string(runes[:300]) + "…"
	}
	if detail == "" {
		return fmt.Sprintf("%s rejected with code %v", what, code)
	}
	return fmt.Sprintf("%s rejected with code %v: %s", what, code, detail)
}
