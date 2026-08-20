package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
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
	got, err := mapUrlResponse([]byte(urlFixture), "high")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "3339230677" {
		t.Errorf("id: got %q", got.ID)
	}
	if got.URL != "http://m701.music.126.net/x.mp3" {
		t.Errorf("url: got %q", got.URL)
	}
	if got.Quality != "high" {
		t.Errorf("quality: want the tier that answered, got %q", got.Quality)
	}
}

func TestMapUrlResponseNullIsUpstreamRejection(t *testing.T) {
	// A paid / region-locked track answers 200 with a null url. That is
	// an upstream refusal, not a normal result: returning it as an empty
	// string would hand mpv nothing to play and read as our bug.
	_, err := mapUrlResponse([]byte(urlNullFixture), "high")
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
	// is {"schema_version":N,"data":...}.
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
	// Asserted against the constant, not a literal: the version is
	// declared once and this pins that the envelope carries it.
	if probe.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version: want %d, got %d", SchemaVersion, probe.SchemaVersion)
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

func TestQualityTierBitrates(t *testing.T) {
	// The tier vocabulary is the CONTRACT both resolvers accept and
	// answer in. A caller asks for a tier, never for a bitrate: qq's
	// upstream has no bitrate knob at all, so bps could never be the
	// shared word.
	for _, tier := range qualityTiers {
		if tierBitrate(tier) <= 0 {
			t.Errorf("tier %q must map to a positive bitrate", tier)
		}
	}
	if tierBitrate("lossless") <= tierBitrate("high") {
		t.Error("lossless must request more than high")
	}
	if tierBitrate("high") <= tierBitrate("standard") {
		t.Error("high must request more than standard")
	}
}

func TestLadderFromStartsAtTheRequestedTier(t *testing.T) {
	// Asking for standard must NOT probe lossless: a caller that picked
	// the cheap tier gets the cheap tier, not a silent upgrade.
	if got := ladderFrom("standard"); len(got) != 1 || got[0] != "standard" {
		t.Errorf("standard ladder: got %v", got)
	}
	if got := ladderFrom("lossless"); len(got) != 3 || got[0] != "lossless" {
		t.Errorf("lossless ladder: got %v", got)
	}
	// The default walks the whole ladder top-down.
	if got := ladderFrom(""); len(got) != 3 || got[0] != "lossless" {
		t.Errorf("default ladder: got %v", got)
	}
}

func TestLadderFromUnknownTierIsEmpty(t *testing.T) {
	// An unknown tier is caller error, reported by the verb — not
	// silently coerced to a default the caller did not ask for.
	if got := ladderFrom("ultra"); got != nil {
		t.Errorf("unknown tier must yield no ladder, got %v", got)
	}
}

func TestMapUrlResponsePublishesTierAndBitrate(t *testing.T) {
	// Both halves ride the wire: the tier is what the panel offers, the
	// bitrate is what actually came back. Publishing only the tier would
	// hide a 320k stream answered under a lossless request.
	got, err := mapUrlResponse([]byte(urlFixture), "high")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Quality != "high" {
		t.Errorf("quality must name the tier that answered, got %q", got.Quality)
	}
	if got.Bitrate != 320 {
		t.Errorf("bitrate must be published in kbps, got %d", got.Bitrate)
	}
}

func TestMapUrlResponseUnknownBitrateIsZero(t *testing.T) {
	// Upstream sometimes answers a playable url with br 0. That is
	// "unknown", published as 0 rather than invented.
	body := `{"data":[{"id":1,"url":"http://x/y.mp3","br":0}],"code":200}`
	got, err := mapUrlResponse([]byte(body), "standard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Bitrate != 0 {
		t.Errorf("unknown bitrate must publish as 0, got %d", got.Bitrate)
	}
}

func TestUpstreamLoggerIsSilenced(t *testing.T) {
	// The upstream library logs full request internals — url, method,
	// encrypted params, response body, cookies — to the standard logger
	// on ANY non-200. That writes to stderr, which this CLI's contract
	// reserves for ONE error-envelope line: a caller parses stderr as
	// JSON, so a log line prepended to it costs the error CLASS, and the
	// dump leaks a live session's cookies into whatever collects logs.
	silenceUpstreamLogger()

	var probe bytes.Buffer
	log.SetOutput(&probe)
	defer log.SetOutput(os.Stderr)
	// Re-silencing must survive a caller that reset the output, which is
	// why the guard is the flags/prefix-independent io.Discard target
	// rather than a one-shot at startup.
	silenceUpstreamLogger()
	log.Printf("this must not reach any writer a caller reads")

	if probe.Len() != 0 {
		t.Errorf("standard logger still writes: %q", probe.String())
	}
}

func TestRejectedCarriesTheBodyDetail(t *testing.T) {
	// The library reports transport failures as sentinel code 520 with
	// the underlying error text in the BODY (dns timeout, dial refused).
	// Dropping it turns an actionable failure into a bare number.
	msg := rejected("search", 520, []byte("lookup music.163.com: i/o timeout"))
	want := "search rejected with code 520: lookup music.163.com: i/o timeout"
	if msg != want {
		t.Errorf("want %q, got %q", want, msg)
	}
}

func TestRejectedWithoutBodyStaysBare(t *testing.T) {
	msg := rejected("search", 404, []byte("  \n"))
	if msg != "search rejected with code 404" {
		t.Errorf("got %q", msg)
	}
}

func TestRejectedTruncatesOversizedBodies(t *testing.T) {
	// A real HTTP error body can be a whole HTML page; the envelope
	// message stays one readable line.
	long := strings.Repeat("宽", 400)
	msg := rejected("search", 502, []byte(long))
	if utf8.RuneCountInString(msg) >= utf8.RuneCountInString("search rejected with code 502: ")+400 {
		t.Errorf("body was not truncated: %d runes", utf8.RuneCountInString(msg))
	}
	if !strings.HasSuffix(msg, "…") {
		t.Errorf("truncation must be visible, got tail %q", msg[len(msg)-12:])
	}
	if !utf8.ValidString(msg) {
		t.Error("truncation split a rune")
	}
}
