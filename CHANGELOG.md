# Changelog

## 3.12.0

### Added

- **Channel filters.** `--group-regex`, `--channel-regex`,
  `--group-exclude-regex` and `--channel-exclude-regex` (`GROUP_REGEX`...)
  keep the live channels you want, by group and by name, with regular
  expressions. They apply to the M3U and Xtream playlists, to the live
  categories and channels of the Xtream API, and to the guide: `xmltv.php`
  only holds the channels kept and their programmes, filtered as it streams
  (a compressed guide included). Without filters, answers are passed on as
  before. (#162; option names from #59, thanks @rodriguezst, and the idea of
  #57, thanks @p418)
- `--listen-address` (`LISTEN_ADDRESS`): the IP address the proxy listens on,
  instead of every interface. (#136)
- The Docker tags `latest`, `v3`, `v3.12` and `v3.12.0` now hold both amd64
  and arm64 images: a Raspberry Pi pulls the right one. The `-amd64` and
  `-arm64` tags stay.

### Fixed

- Provider credentials written outside of the track addresses of a playlist
  reached the players: a guide address in `url-tvg`, a catch-up address in
  `catchup-source`, a player option. In an Xtream playlist these addresses
  now point to the proxy (catch-up plays through it); in an M3U playlist, an
  attribute or line that names the provider's password is removed.
- In the Xtream API, an address of the provider's own API (a guide address)
  now points to the proxy instead of keeping the provider's host.
- `hostname`, `user` and `password` in the configuration file lost to the
  system's `HOSTNAME` and `USER` variables. The file now wins over the old
  variable names; `PROXY_HOSTNAME`, `PROXY_USER`, `PROXY_PASSWORD` and flags
  still win over the file. (#113)
- The examples use `PROXY_HOSTNAME`, `PROXY_USER` and `PROXY_PASSWORD`.

## 3.11.0

### Added

- `--xtream-api-get-movies` (`XTREAM_API_GET_MOVIES`): adds the provider's
  movies to the playlist generated from its API, each with its own file
  format. Off by default, as a catalogue of tens of thousands of movies makes
  a playlist some players cannot load. Series are not added: the API lists
  the episodes of one series at a time. (from #182, thanks @bromeroarbelaez)
- `PROXY_USER`, `PROXY_PASSWORD` and `PROXY_HOSTNAME`: environment variables
  that cannot be mistaken for the system's. `USER` is set by shells to the
  login name and `HOSTNAME` by Docker to the container's id, which the proxy
  then took as its own settings. The old names keep working; the new ones
  win when both are set.

### Fixed

- With the Xtream options alone (no `--m3u-url`), `/iptv.m3u` was an empty
  playlist. It is now the account's playlist, as `/get.php`.
- The configuration file `.iptv-proxy.yaml` is looked for in the home and
  current directories when `--iptv-proxy-config` is not given, as its help
  said. The option's default was a literal `C`, so the file was never read.

## 3.10.0

### Added

- **One provider connection per live stream, whatever the number of
  clients.** An account allows a few connections, often a single one: two
  devices on the same channel now share one connection to the provider. The
  first client opens the stream, the next ones join it where it is, and the
  provider's stream is closed as soon as the last one leaves. Movies,
  episodes, catch-up and anything asked with a range are files: each client
  still reads its own. `--no-stream-sharing` (`NO_STREAM_SHARING`) goes back
  to one connection per client. (related: #110, #101)
- **A live stream the provider drops is opened again** while clients are
  watching, without the player having to reconnect: at once, then after
  0.5, 1, 2 and 4 seconds. A provider that keeps dropping it is given up. A
  connection that stays open but sends nothing for 20 seconds (a frozen
  picture) is opened again too. (related: #125)
- MPEG-TS is forwarded by whole packets, so a client joining a stream, or a
  connection opened again, never starts in the middle of a packet.

### Changed

- A client that cannot keep up with a live stream is disconnected instead
  of delaying it: it must not hold the others back.
- Media starts a little sooner: only the first 16 bytes of a provider answer
  are looked at to tell a playlist from media.

### Security

- gin's debug mode is no longer the default: at startup it listed the
  routes, which hold the proxy's user and password. `GIN_MODE=debug` brings
  it back.

## 3.9.0

### Fixed

- **HLS works whatever the provider.** Every address an HLS playlist names
  (variant playlists, segments, keys, initialization sections, alternate
  audio) is now served by the proxy, on any host and after any redirect,
  with its query string. It used to work only for one provider's address
  scheme; anything else ended in a 404. This covers Xtream streams asked as
  `.m3u8` and M3U tracks alike. (related: #168, #135, #157)
- A playlist asked with `Range: bytes=0-`, as players do, is no longer cut
  short.

### Security

- **A provider's error page could reach a client with the provider's
  credentials in it** (3.8.0 and earlier). Such a page often repeats the
  address it was asked, which holds them. Error answers of the provider (API,
  streams, guide) now have its user and password replaced by the proxy's, in
  every form an address or a page carries them, and a response header holding
  the password is dropped. Only someone with the proxy's own credentials could
  see such a page.
- In API answers, the provider's credentials given as query parameters are
  rewritten whatever their order.

### Changed

- Addresses in HLS playlists are opaque: `/hls/<token>/<name>`. The token is
  the provider's address, encrypted with a key derived from the proxy's
  configuration. A client never sees the provider's host, credentials or
  session tokens, and cannot make the proxy fetch an address of its own.
  Nothing is stored: tokens stay valid across restarts.
- The provider-specific routes `/hlsr/...` and the previous `/hls/...` are
  gone; a player holding a playlist from an older version just reloads it.

### Internal

- New `pkg/hls` package (playlist rewriting), fuzzed in CI.
- Checked with ffmpeg as a player on public test streams (MPEG-TS and fMP4
  master playlists).

## 3.8.0

A maintenance release: the same proxy and the same options, on a base that
no longer depends on how a given provider formats its answers.

### Fixed

- **Xtream API answers are passed on as the provider wrote them.** They used
  to be decoded into fixed structures and encoded again, which failed as soon
  as a provider sent a string where a number was expected (or the other way
  round), base64 that was not base64, or an object instead of an array, and
  which dropped every field the proxy did not know. Only what names the
  provider is rewritten now: the account and server of the login answer, and
  stream addresses holding its credentials.
  (related: #160, #114, #140, #147, #115, #161, #171, #111, #156)
- **The guide (`xmltv.php`) is streamed** from the provider instead of being
  loaded in memory. (related: #155, #181)
- **HLS playlists a provider serves without a redirect** came back empty;
  they are now served. A redirect the proxy cannot serve from is handed to
  the client instead of failing. (related: #168, #135; thanks @rustyx for #183)
- **M3U playlists keep every line of the provider**: empty `tvg-id`, the
  `url-tvg` of the header, `#EXTGRP`, `#EXTVLCOPT`, names with a comma, logos
  inlined as base64. (related: #169, #150, #133, #165)
- **M3U tracks whose address has a query string** (a token) were unreachable.
- **With an Xtream provider, the playlist is no longer downloaded at
  startup**: the proxy starts even when `get.php` is slow or disabled.
- **A stream stops at the provider when the client leaves**, instead of
  holding one of the account's connections.
- A provider gets up to five minutes to start sending a playlist or a guide
  (large catalogues are generated on request), and thirty seconds to start a
  stream.
- Connection headers (`Connection`, `Keep-Alive`, `Transfer-Encoding`...) are
  no longer copied between the client and the provider.

### Security

- The Xtream password is no longer written to the log at startup, and
  provider addresses (which hold the credentials) are kept out of error logs.
- A provider page refusing a playlist is no longer relayed to the log.
- The proxy's own user and password are masked in the access log (the query
  of API requests, the path of streams): logs are safe to share in an issue.
- Credentials are compared in constant time.

### Added

- `--user-agent` (`USER_AGENT`): the User-Agent sent to the provider instead
  of the client's. Without it, requests the proxy makes on its own (the
  playlist at startup) identify as VLC rather than as Go's HTTP client, which
  many providers refuse. (related: #132, #142)

### Internal

- Go 1.27, dependencies up to date, no vendored code: the two libraries that
  parsed playlists and Xtream answers are replaced by two small packages
  (`pkg/m3u`, `pkg/xtream`).
- Docker images are published to Docker Hub (`pierro777/iptv-proxy`) and to
  `ghcr.io/pierre-emmanuelj/iptv-proxy`, which replaces the retired
  `docker.pkg.github.com` registry.
- A test suite: a fake provider with real-world quirks, checks that the
  provider's credentials never reach a client or a log, and fuzzing of the
  playlist reader. CI runs it with the race detector and a strict linter.
