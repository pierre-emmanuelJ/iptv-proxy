# Changelog

## 4.7.1

### Fixed

- **The HDHomeRun tuner with several sources** announced the connections of
  the first account only. It now announces those of every source together
  (a source's `max-connections`, else what its account allows, one for an
  account that does not say): a media server can watch as many channels at
  once as the sources allow.

## 4.7.0

### Added

- **HLS channels on the HDHomeRun tuner**, with ffmpeg. Media servers expect
  MPEG-TS; a channel the provider has only as HLS (an M3U playlist of
  `.m3u8` addresses, or an Xtream account whose `allowed_output_formats` has
  no `ts`, which is then asked for HLS) is read by ffmpeg and written as
  MPEG-TS, without encoding it again. Viewers of a channel share one ffmpeg;
  when ffmpeg fails on an address, the channel's next one is tried. ffmpeg's
  messages are logged without addresses.
- **`-ffmpeg` images**: each Docker tag has one with ffmpeg (`latest-ffmpeg`,
  `v4-ffmpeg`, `v4.7.0-ffmpeg`...), for amd64 and ARM. The usual images do
  not change.
- `--ffmpeg` (`FFMPEG`): the ffmpeg to use. By default `ffmpeg`, used when it
  is found; `none` goes without.

## 4.6.0

### Added

- **Channel logos in Plex's guide.** Plex's apps are served over HTTPS and
  refuse a logo at a plain `http://` address, which many providers have
  only: the guide showed no logos. When the proxy's own address is HTTPS,
  the tuner's guide now gives those logos through the proxy
  (`/logo/<token>/<name>`, as `--proxy-logos` does for players).
- **`--proxy-logos` covers the guides too**: the images of `xmltv.php` (and
  of an M3U playlist's guide) and of the tuner's guide come through the
  proxy.

## 4.5.0

### Changed

- **The HDHomeRun tuner lists a channel once per guide id.** A channel the
  provider lists several times with the same guide id (another quality, a
  backup) is one tuner channel, the first of the list; the others are its
  fallbacks, played in turn when it fails. With an M3U playlist, the guide
  id is the track's `tvg-id`. Channels without a guide id are all kept.

### Fixed

- **Adding the DVR in Plex failed at its last step** ("try again later")
  with a tuner of many channels. Plex refuses the channel mapping of more
  than about 400 channels; providers often list each channel in three or
  four qualities. On the account this was found with, the tuner went from
  565 channels (441 with a guide, for 200 guide ids) to 324, and its guide
  from 9.7 MB to 3.3 MB.
- **Plex showed the tuner's channels by number** ("634532") instead of their
  name. The tuner's guide gives each channel its number as a name too, as
  media servers match them; it now comes after the channel's own names, and
  Plex shows the first.

## 4.4.0

### Added

- **A read-only status page** at `/status` (and `/status.json`), with
  `--status-password` (`STATUS_PASSWORD`). It shows:
  - the streams being watched, by whom, from where and for how long;
  - the users and their limits;
  - the live streams open at the provider;
  - the sources and their streams;
  - the HDHomeRun tuner;
  - the kept answers.

  It shows no credentials.
- `iptv-proxy --version`.

## 4.3.0

### Added

- **Several sources**, with failover. Accounts listed under `sources` in the
  configuration file are served with the one of the Xtream options as one
  catalogue: the first source keeps its ids, the others are shown in ranges
  of their own (source n: n × 100000000 + id); categories of the same name
  are one; a live channel several sources have (the same guide id) is shown
  once and plays from the next source when it fails or when its source has
  no connection left. Movies and series of every source are listed. The
  guide adds the channels only the other sources have.

## 4.2.0

### Added

- **Filters of the HDHomeRun tuner alone**: `--hdhomerun-group-regex`,
  `--hdhomerun-channel-regex`, `--hdhomerun-group-exclude-regex` and
  `--hdhomerun-channel-exclude-regex` (`HDHOMERUN_GROUP_REGEX`...), applied
  after the proxy's filters. A media server can get a few groups while
  players keep every channel; before, the tuner only had the proxy's
  filters, which hold for everyone.

## 4.1.0

### Added

- **Each player with its own provider account** (`--xtream-passthrough`,
  `XTREAM_PASSTHROUGH`). The proxy is given only the provider's address;
  players log in with their own account of the provider, which the proxy
  checks with the provider (again every 10 minutes, and an account it
  accepted keeps playing while the provider fails). Each account is held to
  the streams the provider allows it, with the same-device rule of 4.0, or to
  `--max-connections`. Playlists, API answers, the guide and error pages name
  the proxy and leave out the provider's address; the access log masks the
  accounts. (#72)
## 4.0.0

### Added

- **Several users.** A `users` list in the configuration file gives each
  person or device their own name and password, instead of the one of
  `--user` and `--password`. Their playlists, stream addresses and kept
  answers are their own. `IPTV_PROXY_CONFIG` names the configuration file,
  for Docker. (#72, #163)
- **A limit of streams at once per user** (`max-connections`, or
  `--max-connections` for the one user of the flags). At the limit, a new
  stream from the same address replaces that address's oldest one, so
  zapping keeps working; another address is refused (403). The login answer
  gives the user's limit and active streams.
- **Filters per user**, on top of the proxy's own. For a user with filters,
  a live channel left out does not play either, even asked by its id.

### Changed

- The major version: Docker images are now tagged `v4`. Nothing changes for
  a proxy with one user: the same options give the same addresses.
- Provider error pages name the user's credentials in place of the
  provider's, as before; a request with no user (an HLS segment) gets them
  masked.

## 3.14.0

### Added

- **An HDHomeRun tuner for Plex, Emby, Jellyfin and Channels DVR.**
  `--hdhomerun-port` (`HDHOMERUN_PORT`) serves the live channels the filters
  keep as a network TV tuner, on a port of its own meant for the local
  network: `discover.json`, `lineup.json`, the channels at `/auto/v<number>`
  (shared with the proxy's other clients), and `/guide.xml`, the guide with
  each channel under its tuner number so that media servers match it. Its
  answers hold no credentials. It announces as many tuners as the Xtream
  account allows connections, or `--hdhomerun-tuners`. Channel numbers are
  the provider's stream ids (Xtream) or `tvg-chno` (M3U).
- `--proxy-logos` (`PROXY_LOGOS`): the logos and covers of playlists and of
  the Xtream API are served through the proxy, like the streams, so that
  players reach no other host (behind a VPN, for instance). Only the image
  fields of the API answers change; everything else is passed on as it was.
  (#120)

## 3.13.0

### Added

- **The provider's last good answers stand in when it fails.** The login,
  the lists of the Xtream API, the playlists and the guide are kept,
  compressed in memory, as the provider last sent them in full. When it
  answers an error, a "not found", a page that is not what was asked, or
  nothing, players get that answer instead, and the log says so. Refusals
  (401, 403) are passed on.
- **M3U playlists are read again** when a player asks for one older than
  `--m3u-cache-expiration` (one hour by default), with no restart. A
  playlist that cannot be read is kept, and asked again five minutes later.
  (#131, #85)
- **A guide for M3U playlists**: the guide the playlist names (`url-tvg`,
  `x-tvg-url`), or `--xmltv-url` (`XMLTV_URL`, an address or a local file),
  is served at `/xmltv.php`, and the playlist names that address. The
  filters apply to it. (#84)

### Changed

- An M3U track's address on the proxy is derived from its address at the
  provider instead of its position: it stays the same when the playlist
  changes. Without `--custom-id`, the first element of track addresses is
  derived from your settings instead of drawn at random: addresses survive a
  restart. Players load the playlist again once, then keep working.
- A large M3U playlist takes far less memory and starts faster: with a
  playlist of 910,000 tracks (259 MB), 440 MB once loaded instead of 1.4 GB,
  and 5.5 s to start instead of 9.5 s.

### Fixed

- Playlist attribute names are read regardless of case, as players do:
  some providers write `tvg-ID` or `Group-Title`, which the filters missed.

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
