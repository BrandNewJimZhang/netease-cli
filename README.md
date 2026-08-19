# netease-cli

An agent-native resolver for NetEase Cloud Music: read-only verbs that
answer JSON, plus the login lifecycle their session needs. Built for
[AutoSkill](https://github.com/)'s music app but usable by any caller
that speaks shell.

Wraps [go-musicfox/netease-music](https://github.com/go-musicfox/netease-music).

## Install

```bash
go build -o netease-cli .
cp netease-cli /usr/local/bin/    # or anywhere on PATH
```

## Verbs

```bash
netease-cli search --keyword "周杰伦 晴天" --limit 20
netease-cli url    --id 186016 [--quality lossless|high|standard]
netease-cli lyric  --id 186016
netease-cli whoami

netease-cli playlists [--limit 50]
netease-cli playlist  --id 24381616 [--limit 50]
netease-cli daily

netease-cli login start
netease-cli login poll  --identifier <token from start>
netease-cli refresh
```

## Contract

Every success prints one envelope on stdout:

```json
{"schema_version": 2, "data": ...}
```

| Verb     | `data` shape |
|----------|--------------|
| `search` | `[{id, title, artist, album, cover, duration}]` — duration in milliseconds, multiple artists joined with ` / `, `cover` an album-art URL or `""` |
| `url`    | `{id, url, quality, bitrate}` — `quality` is the TIER that answered (`lossless` / `high` / `standard`), `bitrate` the measured kbps (0 when upstream does not say) |
| `lyric`  | `{id, lrc}` — LRC document, `""` when the track has none |
| `whoami` | `{logged_in, nickname, vip}` — who `MUSICFOX_COOKIE` authenticates as; anonymous and rejected cookies both answer `logged_in: false` |
| `playlists` | `[{id, title, cover, count, description}]` — the account's own shelf |
| `playlist` | `[{id, title, artist, album, cover, duration}]` — the SAME row `search` publishes, so a shelf is playable and queueable without a second shape |
| `daily` | same track rows — today's recommendations |
| `login start` | `{identifier, login_type, mimetype, image_base64}` — the QR to render and the token to poll it by |
| `login poll` | `{state, credential}` — `state` is one of `pending` / `scanned` / `done` / `expired`; `credential` is `{cookie}`, non-null only on `done` |
| `refresh` | `{cookie}` — the renewed session, same shape `login poll` publishes on `done` |

Failures print one line on stderr and exit non-zero:

```json
{"error_class": "bad_input", "message": "--keyword is required"}
```

| Exit | Meaning |
|------|---------|
| 0    | ok |
| 3    | bad input (missing/invalid flag, unknown command, nothing to renew) |
| 4    | upstream refused (non-200, unparseable body, no playable url) |

| `error_class`        | Exit | Meaning |
|----------------------|------|---------|
| `bad_input`          | 3    | malformed flag or unknown command |
| `credential_missing` | 3    | `refresh` / `playlists` / `daily` with no signed-in session |
| `credential_expired` | 4    | upstream refused the renewal (code 301) — sign in again |
| `upstream_rejected`  | 4    | non-200, unparseable body, no playable url |

## Scope and limits

- **Sessions.** Search, most lyrics and free-track URLs work
  unauthenticated. For your own account's catalogue, export a session
  into `MUSICFOX_COOKIE` (a Cookie-header string, e.g. `MUSIC_U=...`);
  this CLI parses it and installs it as the request session. That string
  is exactly what `login poll` publishes on `done` and what `refresh`
  republishes — one format, one round trip, no second shape to convert.
- **QR login.** `login start` mints a code and publishes its
  `identifier`; `login poll` checks that identifier once. The identifier
  is the whole of the poll state, so the two verbs run as separate
  processes and the caller polls on its own schedule. Poll until `done`
  (store the credential) or `expired` (mint a new code). NetEase has no
  distinct "user refused" code — a refusal simply lets the code expire —
  so the `refused` state qq-cli can publish never appears here.
- **Account-scoped verbs.** `playlists` and `daily` describe one
  account, so an anonymous session is refused by name rather than
  answering an empty shelf — "you have nothing saved" and "we do not
  know who you are" must not look the same. `playlist` takes a public
  id and works signed out.
- **Playlist paging.** `--limit` caps the published rows. Upstream
  assembles the whole shelf in one call (there is no page to ask for),
  so the cap is applied after the fetch — it bounds what the panel
  renders, not what the network carries. It exists for argv parity with
  qq-cli, whose upstream does page.
- **Renewal.** `refresh` extends the exported session in place; upstream
  answers 301 for a session past renewal, reported as
  `credential_expired` so a caller replaces it instead of retrying.
- **Quality tiers.** `--quality` names a TIER, never a bitrate: the
  sibling resolver's upstream exposes file types with no bitrate knob,
  so bps could never be the shared word. The request starts the ladder
  and falls DOWN only — asking for `standard` never silently upgrades
  you. The answer carries both the tier that landed and its measured
  kbps, so a 320k stream answered under a lossless request is visible
  rather than hidden behind the word.
- **No unlocking.** `SkipUNM` is pinned true, so paid or region-locked
  tracks answer exit 4 rather than being routed around. The
  UnblockNeteaseMusic package arrives as a transitive dependency of the
  upstream library and is never invoked.
- **Reverse-engineered.** NetEase publishes no personal-use API. This
  reads the same endpoints the desktop client uses; upstream can change
  or break at any time.
- **Line-level lyrics only.** The `yrc` word-level (karaoke) field is
  not published.
