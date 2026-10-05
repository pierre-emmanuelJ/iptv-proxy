# iptv-proxy

[![CI](https://github.com/pierre-emmanuelJ/iptv-proxy/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/pierre-emmanuelJ/iptv-proxy/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/pierre-emmanuelJ/iptv-proxy)](https://github.com/pierre-emmanuelJ/iptv-proxy/releases/latest)
[![Docker pulls](https://img.shields.io/docker/pulls/pierro777/iptv-proxy)](https://hub.docker.com/r/pierro777/iptv-proxy)

iptv-proxy is a reverse proxy for IPTV: give it an M3U/M3U8 playlist or an
Xtream Codes account, and it serves the same channels, movies and series from
your own address, with a user and a password you choose.

Your players (TiviMate, IPTV Smarters, VLC, Kodi...) talk to the proxy and
never see the provider's address or credentials. Media servers (Plex,
Jellyfin, Emby, Channels DVR) can see the live channels as an HDHomeRun TV
tuner, with their guide. Use it to share IPTV at home without handing out the
provider's login, to put a provider behind your own domain, HTTPS or VPN, or
to give every device one address that does not change.

It is one binary or one container, set up with a few flags or environment
variables. There is no database and nothing to configure in a web interface;
a read-only status page shows what is being watched.

## Quick start

In the examples, `192.168.1.10` is the machine running the proxy, as your
players reach it, and `family` / `choose-a-password` are the credentials you
give to your players. Replace them with your own.

### Docker, with an M3U playlist

```sh
docker run -d --name iptv-proxy -p 8080:8080 \
  -e M3U_URL="http://provider.example:8080/playlist.m3u" \
  -e PROXY_HOSTNAME=192.168.1.10 \
  -e PROXY_USER=family \
  -e PROXY_PASSWORD=choose-a-password \
  -e GIN_MODE=release \
  pierro777/iptv-proxy:latest
```

Give this address to your player:

```
http://192.168.1.10:8080/iptv.m3u?username=family&password=choose-a-password
```

To check it from a terminal:

```sh
curl "http://192.168.1.10:8080/iptv.m3u?username=family&password=choose-a-password"
```

The playlist can also be a local file: mount it and give its path.

```sh
docker run -d --name iptv-proxy -p 8080:8080 \
  -v "$PWD/iptv.m3u:/iptv.m3u:ro" \
  -e M3U_URL=/iptv.m3u \
  -e PROXY_HOSTNAME=192.168.1.10 \
  -e PROXY_USER=family \
  -e PROXY_PASSWORD=choose-a-password \
  -e GIN_MODE=release \
  pierro777/iptv-proxy:latest
```

### Docker, with an Xtream Codes account

```sh
docker run -d --name iptv-proxy -p 8080:8080 \
  -e XTREAM_BASE_URL="http://provider.example:8080" \
  -e XTREAM_USER=xtream_user \
  -e XTREAM_PASSWORD=xtream_password \
  -e PROXY_HOSTNAME=192.168.1.10 \
  -e PROXY_USER=family \
  -e PROXY_PASSWORD=choose-a-password \
  -e GIN_MODE=release \
  pierro777/iptv-proxy:latest
```

In your player, choose the Xtream Codes login and enter:

```
Server:   http://192.168.1.10:8080
Username: family
Password: choose-a-password
```

A player that only takes a playlist can use the usual Xtream addresses:

```
Playlist: http://192.168.1.10:8080/get.php?username=family&password=choose-a-password&type=m3u_plus&output=ts
Guide:    http://192.168.1.10:8080/xmltv.php?username=family&password=choose-a-password
```

### Docker images

| Registry | Image |
|---|---|
| Docker Hub | `pierro777/iptv-proxy` |
| GitHub | `ghcr.io/pierre-emmanuelj/iptv-proxy` |

Tags: `latest`, and for a given version `v4.7.1`, `v4.7` or `v4` (`v3` for the
3.x versions). Since v3.12.0 they work on amd64 and on ARM (Raspberry Pi, Apple silicon): Docker
pulls the image of your machine. The `-amd64` and `-arm64` tags
(`pierro777/iptv-proxy:latest-arm64`) name one architecture, as before.

Each tag also has an image with ffmpeg, about 140 MB larger: `latest-ffmpeg`,
`v4-ffmpeg`, `v4.7.0-ffmpeg`... Take it if the HDHomeRun tuner serves
channels your provider has only as HLS (see
[Plex and other media servers](#plex-and-other-media-servers)); nothing
else needs it.

### Docker Compose

```yaml
services:
  iptv-proxy:
    image: pierro777/iptv-proxy:latest
    restart: unless-stopped
    ports:
      # the same port as PORT below
      - 8080:8080
    environment:
      # Either an M3U playlist (an address, or a file mounted in the container)...
      M3U_URL: "http://provider.example:8080/playlist.m3u"
      # ...or an Xtream Codes account (remove M3U_URL then)
      # XTREAM_BASE_URL: "http://provider.example:8080"
      # XTREAM_USER: xtream_user
      # XTREAM_PASSWORD: xtream_password
      PORT: 8080
      # Name or IP your players reach this machine at
      PROXY_HOSTNAME: 192.168.1.10
      # What your players log in with
      PROXY_USER: family
      PROXY_PASSWORD: choose-a-password
      GIN_MODE: release
```

```sh
docker compose up -d
```

The [`docker-compose.yml`](docker-compose.yml) of this repository does the
same with an Xtream Codes account, building the image from the sources.

### Binary

Download the archive for your system (Linux, macOS, Windows; amd64 and arm64;
also `.deb` and `.rpm`) from the
[releases](https://github.com/pierre-emmanuelJ/iptv-proxy/releases/latest),
or build it with Go 1.27 or later:

```sh
git clone https://github.com/pierre-emmanuelJ/iptv-proxy.git
cd iptv-proxy
go build -o iptv-proxy .
```

```sh
GIN_MODE=release ./iptv-proxy \
  --m3u-url "http://provider.example:8080/playlist.m3u" \
  --hostname 192.168.1.10 \
  --user family \
  --password choose-a-password
```

Quote the playlist address: it usually contains `?` and `&`.

## Options

Every option is a flag, an environment variable (the flag's name in upper
case, with `_` instead of `-`) or a line of the configuration file. A flag
wins over the variable, and the variable over the file.

| Flag | Environment variable | Default | What it does |
|---|---|---|---|
| `--m3u-url`, `-u` | `M3U_URL` | | Provider playlist: an `http(s)` address or a local file. |
| `--xtream-base-url` | `XTREAM_BASE_URL` | | Provider's Xtream Codes address, e.g. `http://provider.example:8080`. |
| `--xtream-user` | `XTREAM_USER` | | Provider's Xtream Codes user. |
| `--xtream-password` | `XTREAM_PASSWORD` | | Provider's Xtream Codes password. |
| `--xtream-passthrough` | `XTREAM_PASSTHROUGH` | `false` | Players log in with their own account of the provider. See [Each player with its own provider account](#each-player-with-its-own-provider-account). |
| `--hostname` | `PROXY_HOSTNAME` (or `HOSTNAME`) | | Name or IP your players reach the proxy at. It is written in every address the proxy gives out. |
| `--port` | `PORT` | `8080` | Port the proxy listens on. |
| `--listen-address` | `LISTEN_ADDRESS` | every interface | IP address the proxy listens on, e.g. `127.0.0.1` behind a reverse proxy on the same machine. |
| `--advertised-port` | `ADVERTISED_PORT` | value of `--port` | Port written in the addresses the proxy gives out, when it differs from the listening port (behind a reverse proxy, or a different published Docker port). |
| `--https` | `HTTPS` | `false` | Write `https://` instead of `http://` in the addresses the proxy gives out. The proxy itself always listens in plain HTTP: put a reverse proxy in front for TLS. |
| `--user` | `PROXY_USER` (or `USER`) | `usertest` | User your players log in with. |
| `--password` | `PROXY_PASSWORD` (or `PASSWORD`) | `passwordtest` | Password your players log in with. |
| `--max-connections` | `MAX_CONNECTIONS` | `0` (no limit) | Streams that user may watch at once. See [Several users](#several-users). |
| `--m3u-file-name` | `M3U_FILE_NAME` | `iptv.m3u` | Name of the playlist the proxy serves: `http://host:port/iptv.m3u`. |
| `--custom-endpoint` | `CUSTOM_ENDPOINT` | | Prefix put before every path: `http://host:port/<custom-endpoint>/iptv.m3u`. |
| `--custom-id` | `CUSTOM_ID` | derived from your settings | First path element of M3U track addresses. The same settings give the same addresses, restart after restart. |
| `--m3u-cache-expiration` | `M3U_CACHE_EXPIRATION` | `1` | Hours a playlist is kept before reading it again from the provider (M3U playlists included since v3.13.0). |
| `--xmltv-url` | `XMLTV_URL` | the guide the playlist names | Guide (XMLTV) of an M3U playlist: an `http(s)` address or a local file. It is served at `/xmltv.php`. |
| `--xtream-api-get` | `XTREAM_API_GET` | `false` | Build the `get.php` playlist (live channels) from the provider's API, for providers that disabled `get.php`. |
| `--xtream-api-get-movies` | `XTREAM_API_GET_MOVIES` | `false` | Add the provider's movies to that generated playlist. Off by default: a catalogue of tens of thousands of movies makes a playlist some players cannot load. Series are not added (see below). |
| `--user-agent` | `USER_AGENT` | | User-Agent sent to the provider instead of the player's. Some providers only answer known players. |
| `--no-stream-sharing` | `NO_STREAM_SHARING` | `false` | Open one provider connection per player for a live stream, instead of sharing one between the players watching it. |
| `--status-password` | `STATUS_PASSWORD` | | Password of a read-only status page at `/status`. See [Status page](#status-page). |
| `--proxy-logos` | `PROXY_LOGOS` | `false` | Serve the logos and covers of playlists, of the Xtream API and of the guides through the proxy too, so that players reach no other host. |
| `--hdhomerun-port` | `HDHOMERUN_PORT` | | Port of an HDHomeRun tuner for Plex, Emby, Jellyfin or Channels DVR. Local network only. See [Plex and other media servers](#plex-and-other-media-servers). |
| `--hdhomerun-group-regex`, `--hdhomerun-channel-regex`, `--hdhomerun-group-exclude-regex`, `--hdhomerun-channel-exclude-regex` | `HDHOMERUN_GROUP_REGEX`... | | Filters of the tuner alone, applied after the proxy's: the media server gets fewer channels than your players. |
| `--hdhomerun-tuners` | `HDHOMERUN_TUNERS` | the account's connections (the sources' together), else `2` | Number of tuners announced: how many channels a media server plays at once. |
| `--ffmpeg` | `FFMPEG` | `ffmpeg` | ffmpeg the HDHomeRun tuner turns HLS channels into MPEG-TS with, when it is found (the `-ffmpeg` images have it). `none` goes without. |
| `--group-regex` | `GROUP_REGEX` | | Keep only the live channels whose group matches this regular expression. See [Filtering channels](#filtering-channels). |
| `--channel-regex` | `CHANNEL_REGEX` | | Keep only the live channels whose name matches this regular expression. |
| `--group-exclude-regex` | `GROUP_EXCLUDE_REGEX` | | Leave out the live channels whose group matches this regular expression. |
| `--channel-exclude-regex` | `CHANNEL_EXCLUDE_REGEX` | | Leave out the live channels whose name matches this regular expression. |
| `--iptv-proxy-config` | `IPTV_PROXY_CONFIG` | `.iptv-proxy.yaml` in the home or current directory | YAML file holding the same options, named as the flags (`m3u-url: ...`), the [users](#several-users) and the [sources](#several-sources). |
| `--version` | | | Prints the version and exits. |

Good to know:

- Change `--user` and `--password`: the defaults are public.
- `USER` and `HOSTNAME` are also ordinary system variables: a shell sets
  `USER` to your login name, and Docker sets `HOSTNAME` to the container's ID.
  Since v3.11.0, prefer `PROXY_USER`, `PROXY_PASSWORD` and `PROXY_HOSTNAME`:
  they win over the old names, which keep working. Since v3.12.0, `user`,
  `password` and `hostname` in the configuration file also win over the old
  names.
- `GIN_MODE` is not an option of the proxy but of its web framework. Since
  v3.10.0 the proxy runs in `release` mode unless you set it. Before that,
  set `GIN_MODE=release` as the examples do: the debug mode lists the routes
  at startup, and they contain your user and password. The access log masks
  them in both modes.

## M3U playlists

The proxy reads the provider's playlist at startup and serves it at
`/iptv.m3u` with every track address replaced by its own. All other lines
(names, logos, groups, guide IDs, player options) are kept as the provider
wrote them. When a player asks for it and it is older than
`--m3u-cache-expiration` (one hour by default), the playlist is read again: no
restart is needed to pick up the provider's changes. If the provider fails,
the previous playlist is kept.

A track's address on the proxy is derived from its address at the provider:
it stays the same when the playlist is read again, wherever the track moves
in it, and from one start of the proxy to the next.

The provider's playlist:

```m3u
#EXTM3U
#EXTINF:-1 tvg-id="news.example" tvg-name="News" tvg-logo="http://provider.example/logos/news.png" group-title="News",News HD
http://provider.example:8080/live/news/1.ts
#EXTINF:-1 tvg-id="sport.example" tvg-name="Sport" tvg-logo="http://provider.example/logos/sport.png" group-title="Sport",Sport HD
http://provider.example:8080/live/sport/2.m3u8?token=abc
```

What your players get:

```m3u
#EXTM3U
#EXTINF:-1 tvg-id="news.example" tvg-name="News" tvg-logo="http://provider.example/logos/news.png" group-title="News",News HD
http://192.168.1.10:8080/e3c0c308/family/choose-a-password/4f2a9c1d7b21e0aa/1.ts
#EXTINF:-1 tvg-id="sport.example" tvg-name="Sport" tvg-logo="http://provider.example/logos/sport.png" group-title="Sport",Sport HD
http://192.168.1.10:8080/e3c0c308/family/choose-a-password/9b03e5c2d8a17f46/2.m3u8
```

`e3c0c308` is the `--custom-id`, and `4f2a9c1d7b21e0aa` names the track.
Tokens and other query parameters of the provider's addresses stay on the
proxy.

When the playlist names a guide (`url-tvg` or `x-tvg-url` in its first line),
or `--xmltv-url` gives one, the proxy serves it at
`/xmltv.php?username=...&password=...`, and the playlist names that address
instead. Players load the guide through the proxy, and the filters apply to
it.

Some playlists name the provider's credentials outside of the track
addresses too: a guide address in `url-tvg`, a catch-up address in
`catchup-source`, a player option. Such an attribute or line is removed, so
that the credentials never reach a player. (In an Xtream Codes playlist, they
point to the proxy instead: see below.)

## Xtream Codes

The proxy answers the Xtream Codes client API like the provider does: live,
movies, series, catch-up and the guide. Your players log in with the proxy's
address and credentials, the proxy asks the provider with the real ones.

| | Provider | Proxy |
|---|---|---|
| Server | `http://provider.example:8080` | `http://192.168.1.10:8080` |
| User | `xtream_user` | `family` |
| Password | `xtream_password` | `choose-a-password` |

The provider's answers are passed on as they are, so the fields and quirks of
any provider reach the player. Only what names the provider is rewritten: the
account and server of the login answer, and the addresses holding its
credentials. In the `get.php` playlist, the guide (`url-tvg`) and catch-up
(`catchup-source`) addresses of the provider become the proxy's, and work
through it.

Endpoints served:

| Endpoint | What it is |
|---|---|
| `/player_api.php` | The client API (login, categories, streams, movie and series details, short guide). |
| `/get.php` | The M3U playlist, with the same parameters as the provider's (`type`, `output`). |
| `/xmltv.php` | The full guide (XMLTV), streamed from the provider. |
| `/apiget` | A playlist of the live channels built from the API. |
| `/<user>/<password>/<id>`, `/live/...`, `/movie/...`, `/series/...`, `/timeshift/...` | The streams. |

`/player_api.php`, `/get.php`, `/xmltv.php` and `/apiget` take
`username=...&password=...`.

If you give `--m3u-url` the provider's `get.php` address
(`http://provider.example:8080/get.php?username=xtream_user&password=xtream_password&type=m3u_plus&output=ts`),
the Xtream options are read from it: everything above is served, and that
playlist is also available at `/iptv.m3u`.

With the Xtream options alone, `/iptv.m3u` is the account's playlist too (the
same as `/get.php`).

The playlist built from the API (`/apiget`, or `/get.php` with
`--xtream-api-get`) holds the live channels, and the movies with
`--xtream-api-get-movies`. Series are not in it: the API gives the episodes of
one series at a time, so listing them all would take one request per series.
Players that speak the Xtream API get movies and series from it directly.

## HLS

HLS streams (`.m3u8`) work in both modes, whatever the provider. Every address
an HLS playlist names (variants, segments, keys, audio tracks) is replaced by
an address of the proxy, `/hls/<token>/<name>`, so the whole stream goes
through the proxy, on any host and after any redirect. The token is the
provider's address, encrypted: a player never sees the provider's host,
credentials or session tokens. Nothing is stored, and tokens stay valid across
restarts.

## Several users

One user and password is all most homes need, and the options above give
exactly that. To give each person or device their own login, list the users
in the configuration file (`--iptv-proxy-config`, or `IPTV_PROXY_CONFIG` in
Docker):

```yaml
# /config/iptv-proxy.yaml
users:
  - name: family
    password: choose-a-password
    max-connections: 2
  - name: kids
    password: another-password
    max-connections: 1
    group-regex: "(?i)kids|cartoon"
  - name: grandma
    password: a-third-one
    group-regex: "^FR "
    channel-exclude-regex: "(?i)adult"
```

```sh
docker run -d --name iptv-proxy -p 8080:8080 \
  -v "$PWD/iptv-proxy.yaml:/config/iptv-proxy.yaml:ro" \
  -e IPTV_PROXY_CONFIG=/config/iptv-proxy.yaml \
  -e XTREAM_BASE_URL="http://provider.example:8080" \
  -e XTREAM_USER=xtream_user \
  -e XTREAM_PASSWORD=xtream_password \
  -e PROXY_HOSTNAME=192.168.1.10 \
  pierro777/iptv-proxy:latest
```

- When `users` is there, those users replace `--user` and `--password`.
  The other options (provider, address, filters) stay where they are: flags,
  variables or the same file.
- **Each user logs in with their own name and password**, and their playlists
  and stream addresses hold their own credentials.
- **`max-connections`** is how many streams the user watches at once (none
  means no limit). At the limit, a new stream from the same device (the same
  address) replaces that device's oldest one: zapping keeps working. A stream
  from another device is refused until one stops. The login answer gives
  players the user's limit and the streams they watch. HLS segments are not
  counted: a live HLS channel is many short requests.
- **Filters** (`group-regex`, `channel-regex`, `group-exclude-regex`,
  `channel-exclude-regex`) apply to that user on top of the proxy's own, with
  the same rules (see [Filtering channels](#filtering-channels)): a live
  channel left out does not play, even asked by its id. Movies and series are
  not filtered.
- Names must not be `hls`, `logo`, `live`, `movie`, `series`, `timeshift` or
  `play`: they are the first elements of the proxy's own addresses.
- `--max-connections` (`MAX_CONNECTIONS`) sets the limit of the one user of
  `--user` and `--password`, without a file.

## Each player with its own provider account

When everyone in the house has their own account with the same provider,
the proxy does not need one: with `--xtream-passthrough`, players log in to
the proxy with their provider account, and the proxy only gives the
provider's address.

```sh
docker run -d --name iptv-proxy -p 8080:8080 \
  -e XTREAM_BASE_URL="http://provider.example:8080" \
  -e XTREAM_PASSTHROUGH=true \
  -e PROXY_HOSTNAME=192.168.1.10 \
  pierro777/iptv-proxy:latest
```

- Players are set up with the proxy's address and **their own provider user
  and password**. Their playlists, guide and stream addresses name the proxy
  and hold their own account; the provider's address is left out.
- The proxy asks the provider whether it accepts an account at the first
  request, then again every 10 minutes. An account refused by the provider
  gets a 401. An account it accepted keeps playing while the provider cannot
  answer, with the [last good answers](#when-the-provider-fails).
- **Each account is held to the streams the provider allows it**
  (`max_connections` of its login answer), with the same rule as
  [several users](#several-users): zapping on the same device works, another
  device waits. `--max-connections` sets another limit for every account.
- The proxy's filters apply to every account. Live streams are shared
  between the players of the same account only.
- There are no proxy users: `--xtream-user`, `--xtream-password`, `users` in
  the configuration file, `--m3u-url` and the HDHomeRun tuner do not go with
  it, and the proxy says so at startup. `--user` and `--password` are not
  used.

## Several sources

A second account, at the same provider or another, can back up the first:
list it under `sources` in the configuration file. The proxy then serves
both accounts as one catalogue.

```yaml
# /config/iptv-proxy.yaml
sources:
  - name: backup
    xtream-base-url: http://other-provider.example:8080
    xtream-user: other_user
    xtream-password: other_password
    max-connections: 1   # optional: the account's own limit by default
```

The Xtream options (`XTREAM_BASE_URL`, `XTREAM_USER`, `XTREAM_PASSWORD`)
stay the first source; without them, the first of `sources` is.

- **Ids.** The first source keeps its ids: the players set up before see
  the same channels under the same numbers. The second source's ids are
  shown as 100000000 + id, the third's as 200000000 + id, and so on.
- **Categories of the same name are one**, with the first source's id.
- **A live channel several sources have is shown once**, from the first of
  them. Channels are recognised by their guide id (`epg_channel_id`): a
  channel without one is shown from each source.
- **Failover.** When a live channel fails at its source (an error, an HTTP
  error), the proxy opens it at the next source that has it. A source
  already holding as many streams as its account allows is passed over:
  its channels play from another source while its connections are busy.
- Movies and series of every source are listed, each from its own source.
- The guide is the first source's, plus the channels only the other sources
  have. The playlist (`get.php`, `iptv.m3u`) is built from the merged
  catalogue, as with `--xtream-api-get`.
- A source that does not answer is left out of the lists for that request;
  the last complete answer is served instead when there is one.
- Sources are Xtream accounts: they do not go with `--m3u-url` or with
  `--xtream-passthrough`.

## Plex and other media servers

With `--hdhomerun-port`, the proxy also answers as an HDHomeRun network tuner
on that port. Plex, Emby, Jellyfin and Channels DVR then see your live
channels as a TV tuner, with a guide matched to them, and can record.

```sh
docker run -d --name iptv-proxy -p 8080:8080 -p 192.168.1.10:5004:5004 \
  -e XTREAM_BASE_URL="http://provider.example:8080" \
  -e XTREAM_USER=xtream_user \
  -e XTREAM_PASSWORD=xtream_password \
  -e PROXY_HOSTNAME=192.168.1.10 \
  -e PROXY_USER=family \
  -e PROXY_PASSWORD=choose-a-password \
  -e HDHOMERUN_PORT=5004 \
  -e HDHOMERUN_GROUP_REGEX='^(FR|UK) ' \
  pierro777/iptv-proxy:latest
```

In Plex: **Settings > Live TV & DVR > Set up Plex DVR**, then "Don't see your
HDHomeRun device? Enter its network address manually": `192.168.1.10:5004`.
When Plex asks for the guide, choose the XMLTV option and give
`http://192.168.1.10:5004/guide.xml`.

When Plex runs in Docker on the same machine, put both containers on one
network and give Plex `iptv-proxy:5004` and `http://iptv-proxy:5004/guide.xml`
instead: the tuner's port then needs no publishing at all.

- **A tuner has no login**: anyone who reaches its port can watch. Keep it on
  your local network: do not publish it on the Internet or behind your
  reverse proxy. In exchange, nothing the tuner answers holds your proxy's or
  your provider's credentials.
- The tuner serves the live channels the filters keep, and only those. Use
  them: a media server is not made for 10,000 channels. Besides the proxy's
  filters, the tuner has its own (`--hdhomerun-group-regex`,
  `--hdhomerun-channel-regex`, `--hdhomerun-group-exclude-regex`,
  `--hdhomerun-channel-exclude-regex`), applied after them: your players
  keep every channel while the media server gets a few groups.
- **Plex takes no more than about 400 channels** with a guide for a tuner:
  past that, adding the DVR fails at its last step ("try again later"). The
  tuner helps: a channel the provider lists several times with the same
  guide id (another quality, a backup) is one tuner channel, the first of
  the list; the others are its fallbacks, played when it fails. If Plex
  still refuses, narrow the tuner's filters.
- With an Xtream account, a channel's number is its id at the provider: it
  does not change when the provider reorders its list, so recordings stay on
  their channel. With an M3U playlist, it is the track's `tvg-chno`, else its
  place in the playlist.
- The guide at `/guide.xml` names each channel by its tuner number, which is
  how media servers match a guide to channels.
- **Channel logos in Plex.** Plex's apps are served over HTTPS and refuse a
  logo at a plain `http://` address; many providers have only those. When
  the proxy's own address is HTTPS (`--https`, see
  [Behind a reverse proxy](#behind-a-reverse-proxy-with-https)), the tuner's
  guide gives such logos through that address, where the Plex apps load them
  and the proxy fetches them. With `--proxy-logos`, all of them come through
  it.
- The tuner announces as many tuners as your Xtream account allows
  connections, or, with several sources, as their accounts allow together
  (`--hdhomerun-tuners` to change it): a media server never opens more
  streams than that.
- Media servers expect MPEG-TS. With an Xtream account, the tuner asks the
  provider for MPEG-TS. A channel the provider has only as HLS (an M3U
  playlist of `.m3u8` addresses, or an account whose
  `allowed_output_formats` has no `ts`) plays when ffmpeg is at hand: take
  an `-ffmpeg` image (see [Docker images](#docker-images)), or have
  `ffmpeg` in the `PATH` of the binary. ffmpeg then reads the HLS stream and
  writes it as MPEG-TS, without encoding it again; viewers of a channel
  share one ffmpeg. Without ffmpeg, such a channel is passed on as a
  playlist, which media servers do not play.
- Plex finds a tuner by its address, entered by hand: automatic discovery on
  the network is not supported.

## Status page

With `--status-password` (`STATUS_PASSWORD`), the proxy serves a read-only
page at `http://host:port/status`. It asks for that password; any user name
does.

It shows:
- the streams being watched: by whom, from which address, for how long;
- the users and their limits;
- how many live streams are open at the provider, shared between their
  viewers;
- the sources and the streams each holds, when there are several;
- the HDHomeRun tuner's channels;
- the answers kept for when the provider fails.

The page refreshes itself every 10 seconds; `/status.json` gives the same
in JSON. It changes nothing, and shows no password: stream addresses are
masked, sources are named by their host. `iptv-proxy --version` prints the
version.

## When the provider fails

Providers fail now and then: an error for a minute, a maintenance page, a
guide that will not generate. The proxy keeps the last answer the provider
gave in full for the lists players load (the login, categories, channels,
movies, series, details, playlists and the guide), compressed in memory. When
the provider fails, players get that answer instead of an error, and the log
says so. Streams themselves are not kept: when the provider is down, nothing
plays.

A refusal (wrong account, expired subscription) is passed on: it says
something you need to know.

## Filtering channels

Four options keep the live channels you want and leave out the others. Each
takes a [regular expression](https://github.com/google/re2/wiki/Syntax) matched
against a channel's group (`group-title` in a playlist, the category in the
Xtream API) or its name:

- `--group-regex`, `--channel-regex`: keep only what matches.
- `--group-exclude-regex`, `--channel-exclude-regex`: leave out what matches,
  even if it was kept by the first two.

A channel is kept when it passes all of them. The expressions are case
sensitive; start one with `(?i)` to ignore case.

```sh
  -e GROUP_REGEX='^(FR|UK) ' \
  -e GROUP_EXCLUDE_REGEX='(?i)adult|xxx' \
  -e CHANNEL_EXCLUDE_REGEX='(?i)backup|test' \
```

The filters apply to:

- the M3U playlist (`/iptv.m3u`) and the Xtream playlists (`/get.php`,
  `/apiget`): a channel left out is not in it, and in M3U mode it has no
  address on the proxy;
- the live categories and channels of the Xtream API;
- the guide (`/xmltv.php`): only the channels kept, and their programmes, are
  in it. A guide of thousands of channels shrinks to the ones you watch.

Movies and series are not filtered. In a `get.php` playlist that holds movies,
the same rules apply to them, by their group and name.

The filters also hold for streams: a live channel left out does not play,
even asked by its id, and the provider is not asked for it. A channel the
provider just added plays as soon as it passes the filters: when a stream
asks for an id the list does not have, the list is read again (at most once
a minute).

## Live streams: one connection to the provider

An IPTV account allows a few connections at a time, often a single one. When
several players watch the same live channel, the proxy opens one connection to
the provider and shares it: the first player opens the stream, the next ones
join it where it is, and the connection is closed when the last one leaves.

If the provider drops the stream, or leaves it silent for 20 seconds, the
proxy opens it again while someone is watching, so the player does not have
to reconnect. After a few attempts that fail, the stream ends.

Movies, series and catch-up are files: each player reads its own, from where
it wants. `--no-stream-sharing` gives every player its own connection for live
streams too.

## Behind a reverse proxy, with HTTPS

The proxy listens in plain HTTP. To serve it on your own domain with HTTPS,
put a reverse proxy (Traefik, Caddy, nginx...) in front, and tell iptv-proxy
which addresses to give out:

```yaml
    environment:
      PORT: 8080             # where iptv-proxy listens
      PROXY_HOSTNAME: iptv.example.com
      ADVERTISED_PORT: 443   # the port your players use
      HTTPS: 1               # addresses given out start with https://
```

Your players then use `https://iptv.example.com:443/iptv.m3u?username=...&password=...`.

The proxy's user and password travel in every address, so use HTTPS as soon as
the proxy is reachable from the Internet.

A complete example with Traefik and Let's Encrypt is in the
[`traefik`](traefik) folder. From the root of the repository:

```sh
cp -r ./traefik/* .
mkdir -p Traefik/etc/traefik Traefik/log
```

Then replace `iptv.proxyexample.xyz` with your domain in `docker-compose.yml`,
set your e-mail address in `Traefik/traefik.yaml`, and start it:

```sh
docker compose up -d
```

## What it does not do

- It does not provide channels or streams: you need a playlist or an account
  from a provider.
- It does not edit the catalogue: no channel editor, no renaming, no guide
  mapping. Players get what the provider sends.
- It does not transcode, record or cache streams: they are passed on as they
  come. The one exception is the tuner's HLS channels, which ffmpeg turns into
  MPEG-TS, without encoding them again.
- It has no web interface to change anything: the status page only shows.

## Roadmap

Nothing is planned beyond fixes: what comes next depends on what users ask
for. Open an issue for a bug or an idea, or a discussion for a question.

See the [changelog](CHANGELOG.md) for what each release brought.

## Contributing

Bug reports, ideas and pull requests are welcome: see
[CONTRIBUTING.md](CONTRIBUTING.md). Questions go to the
[discussions](https://github.com/pierre-emmanuelJ/iptv-proxy/discussions),
vulnerabilities to [SECURITY.md](SECURITY.md).

## License

[GPL-3.0](LICENSE).

Built with [cobra](https://github.com/spf13/cobra) and
[gin](https://github.com/gin-gonic/gin).

If the project is useful to you, you can buy me a beer:

[![paypal](https://www.paypalobjects.com/en_US/i/btn/btn_donate_LG.gif)](https://www.paypal.com/donate?hosted_button_id=WQAAMQWJPKHUN)
