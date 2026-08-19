package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
)

// Fixtures are trimmed captures of real NetEase responses (probed
// 2026-08-18), so the mappers are pinned against the upstream shape
// rather than an invented one.

const searchFixture = `{"result":{"songs":[
  {"id":3339230677,"name":"晴天","dt":182890,
   "ar":[{"name":"周杰伦"}],"al":{"name":"叶惠美","picUrl":"https://p1.music.126.net/cover.jpg"}},
  {"id":186016,"name":"晴天娃娃","dt":210000,
   "ar":[{"name":"歌手A"},{"name":"歌手B"}],"al":{"name":"专辑B"}}
]},"code":200}`

const urlFixture = `{"data":[{"id":3339230677,
  "url":"http://m701.music.126.net/x.mp3","br":320000,"type":"mp3"}],"code":200}`

const urlNullFixture = `{"data":[{"id":1,"url":null,"br":0}],"code":200}`

const accountFixture = `{"code":200,
  "account":{"id":123,"vipType":11},
  "profile":{"userId":123,"nickname":"Jim","vipType":11}}`

const accountAnonymousFixture = `{"code":200,"account":null,"profile":null}`

const lyricFixture = `{"lrc":{"lyric":"[00:01.00]故事的小黄花\n[00:12.50]从出生那年"},"code":200}`

func TestMapSearchResponse(t *testing.T) {
	tracks, err := mapSearchResponse([]byte(searchFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d", len(tracks))
	}
	first := tracks[0]
	if first.ID != "3339230677" {
		t.Errorf("id: want string 3339230677, got %q", first.ID)
	}
	if first.Title != "晴天" {
		t.Errorf("title: got %q", first.Title)
	}
	if first.Artist != "周杰伦" {
		t.Errorf("artist: got %q", first.Artist)
	}
	if first.Album != "叶惠美" {
		t.Errorf("album: got %q", first.Album)
	}
	if first.Duration != 182890 {
		t.Errorf("duration: want ms 182890, got %d", first.Duration)
	}
	if first.Cover != "https://p1.music.126.net/cover.jpg" {
		t.Errorf("cover: got %q", first.Cover)
	}
	// Several artists join with " / " so the row stays one line.
	if tracks[1].Artist != "歌手A / 歌手B" {
		t.Errorf("multi-artist join: got %q", tracks[1].Artist)
	}
	// A track whose album carries no art publishes "", not a fabricated URL.
	if tracks[1].Cover != "" {
		t.Errorf("missing cover must stay empty, got %q", tracks[1].Cover)
	}
}

func TestParseCookieHeader(t *testing.T) {
	cookies, err := parseCookieHeader("MUSIC_U=abc123; __csrf=x9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 2 {
		t.Fatalf("want 2 cookies, got %d", len(cookies))
	}
	if cookies[0].Name != "MUSIC_U" || cookies[0].Value != "abc123" {
		t.Errorf("first cookie: got %s=%s", cookies[0].Name, cookies[0].Value)
	}
	// Domain-scoped so the jar answers for every netease subdomain.
	if cookies[0].Domain != ".music.163.com" {
		t.Errorf("domain: got %q", cookies[0].Domain)
	}
}

func TestParseCookieHeaderMalformedPairRaises(t *testing.T) {
	// A dropped MUSIC_U would read as "logged in but every track
	// refused" — malformed input must fail loudly, not shrink.
	if _, err := parseCookieHeader("MUSIC_U"); err == nil {
		t.Fatal("pair without '=' must raise")
	}
	if _, err := parseCookieHeader(" ; ; "); err == nil {
		t.Fatal("no pairs at all must raise")
	}
}

func TestMapSearchResponseEmpty(t *testing.T) {
	tracks, err := mapSearchResponse([]byte(`{"result":{"songs":[]},"code":200}`))
	if err != nil {
		t.Fatalf("an empty result is valid, got error: %v", err)
	}
	if len(tracks) != 0 {
		t.Fatalf("want 0 tracks, got %d", len(tracks))
	}
}

func TestMapSearchResponseCorrupt(t *testing.T) {
	if _, err := mapSearchResponse([]byte(`not json`)); err == nil {
		t.Fatal("corrupt body must raise, not degrade to empty")
	}
}

func TestMapUrlResponse(t *testing.T) {
	got, err := mapUrlResponse([]byte(urlFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "3339230677" {
		t.Errorf("id: got %q", got.ID)
	}
	if got.URL != "http://m701.music.126.net/x.mp3" {
		t.Errorf("url: got %q", got.URL)
	}
	if got.Quality != "320k" {
		t.Errorf("quality: want 320k, got %q", got.Quality)
	}
}

func TestMapUrlResponseNullIsUpstreamRejection(t *testing.T) {
	// A paid / region-locked track answers 200 with a null url. That is
	// an upstream refusal, not a normal result: returning it as an empty
	// string would hand mpv nothing to play and read as our bug.
	_, err := mapUrlResponse([]byte(urlNullFixture))
	if err == nil {
		t.Fatal("a null url must raise")
	}
}

func TestMapLyricResponse(t *testing.T) {
	lrc, err := mapLyricResponse([]byte(lyricFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lrc != "[00:01.00]故事的小黄花\n[00:12.50]从出生那年" {
		t.Errorf("lrc: got %q", lrc)
	}
}

func TestMapLyricResponseMissingIsEmpty(t *testing.T) {
	// A track with no lyric is the common case, not a failure.
	lrc, err := mapLyricResponse([]byte(`{"code":200}`))
	if err != nil {
		t.Fatalf("missing lyric must not raise: %v", err)
	}
	if lrc != "" {
		t.Errorf("want empty lrc, got %q", lrc)
	}
}

func TestEnvelopeCarriesSchemaVersion(t *testing.T) {
	// The contract the AutoSkill runner unwraps: every success payload
	// is {"schema_version":1,"data":...}.
	out, err := json.Marshal(newEnvelope([]Track{{ID: "1", Title: "t"}}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe struct {
		SchemaVersion int             `json:"schema_version"`
		Data          json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.SchemaVersion != 1 {
		t.Errorf("schema_version: want 1, got %d", probe.SchemaVersion)
	}
	if len(probe.Data) == 0 {
		t.Error("data must be present")
	}
}

func TestErrorEnvelopeShape(t *testing.T) {
	// stderr contract: one structured {"error_class","message"} line.
	out, err := json.Marshal(newErrorEnvelope("upstream_rejected", "login expired"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe struct {
		ErrorClass string `json:"error_class"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.ErrorClass != "upstream_rejected" || probe.Message != "login expired" {
		t.Errorf("error envelope: got %+v", probe)
	}
}

func TestMapAccountResponseSignedIn(t *testing.T) {
	session, err := mapAccountResponse([]byte(accountFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !session.LoggedIn {
		t.Fatal("signed-in fixture must read LoggedIn")
	}
	if session.Nickname != "Jim" {
		t.Errorf("nickname: got %q", session.Nickname)
	}
	if !session.VIP {
		t.Error("vipType 11 must read VIP")
	}
}

func TestMapAccountResponseAnonymousIsNotAnError(t *testing.T) {
	// Anonymous (or a rejected cookie) answers profile null under a
	// 200 — that IS the "not signed in" verdict the caller needs.
	session, err := mapAccountResponse([]byte(accountAnonymousFixture))
	if err != nil {
		t.Fatalf("anonymous is a valid verdict, got error: %v", err)
	}
	if session.LoggedIn {
		t.Fatal("anonymous must not read LoggedIn")
	}
}

func TestMapAccountResponseCorrupt(t *testing.T) {
	if _, err := mapAccountResponse([]byte(`not json`)); err == nil {
		t.Fatal("corrupt body must raise, not degrade to anonymous")
	}
}

// The four codes NetEase's qrcode/client/login answers, captured from
// the upstream service. 803 is the only one that carries a session.
const qrWaitingFixture = `{"code":801,"message":"等待扫码"}`
const qrScannedFixture = `{"code":802,"message":"待确认","nickname":"Jim"}`
const qrExpiredFixture = `{"code":800,"message":"二维码不存在或已过期"}`
const qrAuthorizedFixture = `{"code":803,"message":"授权登录成功"}`

func TestMapQRStatusNamesEveryEvent(t *testing.T) {
	// Every upstream code maps to a state the caller branches on:
	// "keep polling" (pending/scanned) vs "start over" (expired/
	// refused). The names match qq-cli's — one schema, two resolvers.
	cases := map[string]string{
		qrWaitingFixture:    "pending",
		qrScannedFixture:    "scanned",
		qrExpiredFixture:    "expired",
		qrAuthorizedFixture: "done",
	}
	for fixture, want := range cases {
		state, err := mapQRStatusResponse([]byte(fixture))
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", fixture, err)
		}
		if state != want {
			t.Errorf("state for %s: want %q, got %q", fixture, want, state)
		}
	}
}

func TestMapQRStatusUnknownCodeRaises(t *testing.T) {
	// A code this mapper does not know must stop the flow rather than
	// default to "pending" — a caller told to keep polling a code
	// upstream has stopped honouring spins until its own timeout.
	if _, err := mapQRStatusResponse([]byte(`{"code":999}`)); err == nil {
		t.Fatal("want an error for an unknown status code")
	}
}

func TestMapQRStatusCorrupt(t *testing.T) {
	if _, err := mapQRStatusResponse([]byte("not json")); err == nil {
		t.Fatal("want an error for a corrupt body")
	}
}

func TestRenderCookieHeader(t *testing.T) {
	// The credential this resolver publishes is exactly what it accepts
	// back through MUSICFOX_COOKIE — a round trip, not a second format.
	cookies := []*http.Cookie{
		{Name: "MUSIC_U", Value: "abc"},
		{Name: "__csrf", Value: "xyz"},
	}
	if got := renderCookieHeader(cookies); got != "MUSIC_U=abc; __csrf=xyz" {
		t.Errorf("cookie header: got %q", got)
	}
}

func TestRenderCookieHeaderRoundTripsThroughTheParser(t *testing.T) {
	rendered := renderCookieHeader([]*http.Cookie{{Name: "MUSIC_U", Value: "abc"}})
	parsed, err := parseCookieHeader(rendered)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(parsed) != 1 || parsed[0].Name != "MUSIC_U" || parsed[0].Value != "abc" {
		t.Errorf("round trip lost the session: %+v", parsed)
	}
}

func TestEncodeQRImageProducesAPNG(t *testing.T) {
	// The panel renders one image for either resolver, so this one
	// encodes the login URL rather than publishing a bare link that
	// only netease's flow would carry.
	encoded, err := encodeQRImage("http://music.163.com/login?codekey=abc")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("published image is not base64: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte("\x89PNG")) {
		t.Errorf("published image is not a PNG: % x", raw[:8])
	}
}

// Playlist fixtures, trimmed from real responses (probed 2026-08-19).
const userPlaylistFixture = `{"code":200,"playlist":[
  {"id":123456,"name":"我喜欢的音乐","coverImgUrl":"https://p1.music.126.net/c.jpg",
   "trackCount":238,"description":"自动收藏"},
  {"id":789,"name":"睡前","coverImgUrl":"","trackCount":12,"description":null}
]}`

// AllTracks writes the fetched song details back into the detail body
// under playlist.tracks — the same per-song shape cloudsearch nests
// under result.songs, but at its own path.
const allTracksFixture = `{"code":200,"playlist":{"name":"我喜欢的音乐","tracks":[
  {"id":186016,"name":"晴天","dt":269000,
   "ar":[{"name":"周杰伦"}],"al":{"name":"叶惠美","picUrl":"https://p1.music.126.net/a.jpg"}}
]}}`

const recommendSongsFixture = `{"code":200,"data":{"dailySongs":[
  {"id":3339230677,"name":"稻香","dt":223000,
   "ar":[{"name":"周杰伦"}],"al":{"name":"魔杰座","picUrl":"https://p1.music.126.net/b.jpg"}}
]}}`

func TestMapUserPlaylistResponse(t *testing.T) {
	// The playlist row is the SECOND shared shape (after the track row):
	// qq-cli publishes these exact field names, so one panel renders
	// either resolver's shelf.
	lists, err := mapUserPlaylistResponse([]byte(userPlaylistFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lists) != 2 {
		t.Fatalf("want 2 playlists, got %d", len(lists))
	}
	want := Playlist{
		ID:          "123456",
		Title:       "我喜欢的音乐",
		Cover:       "https://p1.music.126.net/c.jpg",
		Count:       238,
		Description: "自动收藏",
	}
	if lists[0] != want {
		t.Errorf("first playlist: got %+v, want %+v", lists[0], want)
	}
	// A null description is an absent one, not the string "null".
	if lists[1].Description != "" {
		t.Errorf("null description must publish as empty, got %q", lists[1].Description)
	}
}

func TestMapUserPlaylistResponseEmpty(t *testing.T) {
	lists, err := mapUserPlaylistResponse([]byte(`{"code":200,"playlist":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lists) != 0 {
		t.Errorf("empty shelf must publish as empty, got %d", len(lists))
	}
}

func TestMapUserPlaylistResponseCorrupt(t *testing.T) {
	if _, err := mapUserPlaylistResponse([]byte("not json")); err == nil {
		t.Fatal("want an error for a corrupt body")
	}
}

func TestMapPlaylistTracksResponse(t *testing.T) {
	// Playlist tracks publish the SAME row as search, so the panel plays
	// a shelf without a second mapper and queues it without a translation.
	tracks, err := mapPlaylistTracksResponse([]byte(allTracksFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("want 1 track, got %d", len(tracks))
	}
	if tracks[0].ID != "186016" || tracks[0].Title != "晴天" {
		t.Errorf("track: got %+v", tracks[0])
	}
	if tracks[0].Artist != "周杰伦" || tracks[0].Duration != 269000 {
		t.Errorf("track detail: got %+v", tracks[0])
	}
}

func TestMapDailySongsResponse(t *testing.T) {
	tracks, err := mapDailySongsResponse([]byte(recommendSongsFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 1 || tracks[0].ID != "3339230677" {
		t.Fatalf("daily songs: got %+v", tracks)
	}
	if tracks[0].Cover != "https://p1.music.126.net/b.jpg" {
		t.Errorf("cover must ride the same field as search rows: %+v", tracks[0])
	}
}

func TestMapDailySongsResponseEmpty(t *testing.T) {
	// A day with no recommendations is a valid empty state (the account
	// is new, or upstream has nothing yet) — not a fault.
	tracks, err := mapDailySongsResponse([]byte(`{"code":200,"data":{"dailySongs":[]}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("want empty, got %d", len(tracks))
	}
}
