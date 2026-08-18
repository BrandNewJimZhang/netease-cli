# netease-cli

An agent-native resolver for NetEase Cloud Music: three read-only verbs
that answer JSON, built for [AutoSkill](https://github.com/)'s music app
but usable by any caller that speaks shell.

Wraps [go-musicfox/netease-music](https://github.com/go-musicfox/netease-music).

## Install

```bash
go build -o netease-cli .
cp netease-cli /usr/local/bin/    # or anywhere on PATH
```

## Verbs

```bash
netease-cli search --keyword "周杰伦 晴天" --limit 20
netease-cli url    --id 186016 [--quality 320000]
netease-cli lyric  --id 186016
```

## Contract

Every success prints one envelope on stdout:

```json
{"schema_version": 1, "data": ...}
```

| Verb     | `data` shape |
|----------|--------------|
| `search` | `[{id, title, artist, album, cover, duration}]` — duration in milliseconds, multiple artists joined with ` / `, `cover` an album-art URL or `""` |
| `url`    | `{id, url, quality}` |
| `lyric`  | `{id, lrc}` — LRC document, `""` when the track has none |

Failures print one line on stderr and exit non-zero:

```json
{"error_class": "bad_input", "message": "--keyword is required"}
```

| Exit | Meaning |
|------|---------|
| 0    | ok |
| 3    | bad input (missing/invalid flag, unknown command) |
| 4    | upstream refused (non-200, unparseable body, no playable url) |

`error_class` is `bad_input` or `upstream_rejected`.

## Scope and limits

- **No login flow.** Search, most lyrics and free-track URLs work
  unauthenticated. For your own account's catalogue, export its cookie
  into `MUSICFOX_COOKIE` (a Cookie-header string, e.g. `MUSIC_U=...`);
  this CLI parses it and installs it as the request session.
- **No unlocking.** `SkipUNM` is pinned true, so paid or region-locked
  tracks answer exit 4 rather than being routed around. The
  UnblockNeteaseMusic package arrives as a transitive dependency of the
  upstream library and is never invoked.
- **Reverse-engineered.** NetEase publishes no personal-use API. This
  reads the same endpoints the desktop client uses; upstream can change
  or break at any time.
- **Line-level lyrics only.** The `yrc` word-level (karaoke) field is
  not published.
