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
const SchemaVersion = 1

// Track is one search result row.
type Track struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Cover    string `json:"cover"` // album art URL, "" when upstream has none
	Duration int64  `json:"duration"` // milliseconds, as upstream reports
}

// PlayableURL is one resolved stream.
type PlayableURL struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Quality string `json:"quality"`
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

// mapSearchResponse decodes a cloudsearch body into published rows.
//
// An empty song list is a valid empty result; a body that does not
// parse is corrupt and raises (fail-fast: empty and corrupt are
// different states and must not collapse).
func mapSearchResponse(body []byte) ([]Track, error) {
	var parsed struct {
		Result struct {
			Songs []struct {
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
			} `json:"songs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("search response is not valid JSON: %w", err)
	}
	tracks := make([]Track, 0, len(parsed.Result.Songs))
	for _, song := range parsed.Result.Songs {
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
	return tracks, nil
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

// qualityLabel renders an upstream bitrate as the label the panel shows.
func qualityLabel(br int64) string {
	if br <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("%dk", br/1000)
}

// mapUrlResponse decodes a player-url body into one playable stream.
//
// Upstream answers 200 with a null url for a paid or region-locked
// track. That is a refusal, not a result: publishing an empty url would
// hand the player nothing and read as a bug on our side, so it raises.
func mapUrlResponse(body []byte) (PlayableURL, error) {
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
		Quality: qualityLabel(entry.Br),
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
