# netease-cli

[English](README.md) | **简体中文**

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![License: MIT](https://img.shields.io/badge/license-MIT-green)
![Schema](https://img.shields.io/badge/schema__version-2-blue)

面向 agent 的网易云音乐 resolver：以 JSON 应答的只读动词，加上会话
所需的登录生命周期。为 AutoSkill 的音乐应用而建，但任何会说 shell
的调用方都能使用。

封装 [go-musicfox/netease-music](https://github.com/go-musicfox/netease-music)。

## 安装

```bash
go build -o netease-cli .
cp netease-cli /usr/local/bin/    # or anywhere on PATH
```

## 发布产物

`scripts/build-artifact.sh [<goos>-<goarch>]` 构建一个精简、无 CGO 的
二进制到 `dist/`，并打印 marketplace 条目所需的平台标识与 sha256：

```bash
scripts/build-artifact.sh darwin-arm64
scripts/build-artifact.sh linux-amd64
```

摘要与二进制一同打印，因为只发布其中之一，会在用户的机器上、在下载
完成之后才失败。

## 动词

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

## 契约

每次成功都在 stdout 打印一个信封：

```json
{"schema_version": 2, "data": ...}
```

| 动词     | `data` 形状 |
|----------|--------------|
| `search` | `[{id, title, artist, album, cover, duration}]` —— `duration` 为毫秒，多位歌手以 ` / ` 连接，`cover` 为专辑封面 URL 或 `""` |
| `url`    | `{id, url, quality, bitrate}` —— `quality` 是实际应答的档位（`lossless` / `high` / `standard`），`bitrate` 是实测 kbps（上游未说明时为 0） |
| `lyric`  | `{id, lrc}` —— LRC 文档，曲目无歌词时为 `""` |
| `whoami` | `{logged_in, nickname, vip}` —— `MUSICFOX_COOKIE` 认证出的身份；匿名与被拒 cookie 都应答 `logged_in: false` |
| `playlists` | `[{id, title, cover, count, description}]` —— 该账号自己的歌单书架 |
| `playlist` | `[{id, title, artist, album, cover, duration}]` —— 与 `search` 发布的同一行形状，书架无需第二种形状即可播放、入队 |
| `daily` | 同样的曲目行 —— 今日推荐 |
| `login start` | `{identifier, login_type, mimetype, image_base64}` —— 要渲染的二维码与轮询它的 token |
| `login poll` | `{state, credential}` —— `state` 为 `pending` / `scanned` / `done` / `expired` 之一；`credential` 是 `{cookie}`，仅在 `done` 时非空 |
| `refresh` | `{cookie}` —— 续期后的会话，与 `login poll` 在 `done` 时发布的形状相同 |

失败在 stderr 打印一行并以非零退出：

```json
{"error_class": "bad_input", "message": "--keyword is required"}
```

| 退出码 | 含义 |
|------|---------|
| 0    | 成功 |
| 3    | 输入错误（标志缺失/非法、未知命令、无可续期会话） |
| 4    | 上游拒绝（非 200、响应体不可解析、无可播放 url） |

| `error_class`        | 退出码 | 含义 |
|----------------------|------|---------|
| `bad_input`          | 3    | 标志格式错误或未知命令 |
| `credential_missing` | 3    | 未登录会话下调用 `refresh` / `playlists` / `daily` |
| `credential_expired` | 4    | 上游拒绝续期（code 301）—— 请重新登录 |
| `upstream_rejected`  | 4    | 非 200、响应体不可解析、无可播放 url |

## 范围与边界

- **会话。** 搜索、多数歌词与免费曲目取流无需登录。要访问自己账号的
  曲库，把会话导出到 `MUSICFOX_COOKIE`（Cookie 头字符串，如
  `MUSIC_U=...`）；本 CLI 解析后将其安装为请求会话。这个字符串正是
  `login poll` 在 `done` 时发布、`refresh` 再次发布的内容 —— 一种
  格式，一个来回，没有需要转换的第二种形状。
- **扫码登录。** `login start` 铸造二维码并发布其 `identifier`；
  `login poll` 对该 identifier 查询一次。identifier 就是轮询状态的
  全部，所以两个动词可以作为独立进程运行，调用方按自己的节奏轮询。
  轮询直到 `done`（保存凭据）或 `expired`（重新铸码）。网易没有独立
  的「用户拒绝」状态 —— 拒绝只会让码自然过期 —— 所以 qq-cli 可能
  发布的 `refused` 状态在这里永不出现。
- **账号域动词。** `playlists` 与 `daily` 描述的是某一个账号，所以
  匿名会话会被点名拒绝而不是应答一个空书架 —— 「你没有收藏」和
  「我们不知道你是谁」不能看起来一样。`playlist` 接受公开 id，未登录
  可用。
- **歌单分页。** `--limit` 截断发布的行数。上游在一次调用里装配整个
  书架（没有可请求的分页），所以截断发生在拉取之后 —— 它约束面板
  渲染什么，不约束网络传输什么。它的存在是为了与 qq-cli 的 argv
  对齐，后者的上游确实分页。
- **续期。** `refresh` 原地延长导出的会话；超过可续期窗口的会话上游
  应答 301，报告为 `credential_expired`，让调用方替换凭据而不是重试。
- **音质档位。** `--quality` 指的是档位（TIER），永远不是码率：姊妹
  resolver 的上游只暴露文件类型、没有码率旋钮，所以 bps 不可能成为
  共享词汇。请求从指定档位起步、只会向下回落 —— 请求 `standard`
  永远不会被静默升档。应答同时携带落地的档位与实测 kbps，所以
  lossless 请求下应答的 320k 流是可见的，不会藏在词后面。
- **不解锁。** `SkipUNM` 锁定为 true，付费或区域受限曲目应答退出码 4
  而不是被绕行。UnblockNeteaseMusic 包只是上游库的传递依赖，从未被
  调用。
- **逆向实现。** 网易不发布面向个人使用的 API。本工具读取桌面客户端
  使用的相同端点；上游随时可能变更或失效。
- **仅行级歌词。** 不发布 `yrc` 字级（卡拉OK）字段。

## 法律声明

非官方项目，与网易云音乐无从属或背书关系。仅限个人、非商业使用。
它以你的身份认证，读取官方客户端服务于你自己账号的相同端点 ——
在账号权益之内，绝不绕过权益（见上文「不解锁」）。不捆绑、不缓存、
不再分发任何音频；CLI 发布的是服务为你的会话铸造的 URL。你有责任
遵守所在司法辖区内该服务的条款。权利方可通过 issue 提出下架请求，
维护者将及时配合。

## 许可证

[MIT](LICENSE)
