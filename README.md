# iptv-proxy

[![CI](https://github.com/pierre-emmanuelJ/iptv-proxy/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/pierre-emmanuelJ/iptv-proxy/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/pierre-emmanuelJ/iptv-proxy)](https://github.com/pierre-emmanuelJ/iptv-proxy/releases/latest)
[![Docker pulls](https://img.shields.io/docker/pulls/pierro777/iptv-proxy)](https://hub.docker.com/r/pierro777/iptv-proxy)

iptv-proxy is a reverse proxy for IPTV: give it an M3U/M3U8 playlist or an
Xtream Codes account, and it serves the same channels, movies and series from
your own address, with a user and a password you choose.

Your players (TiviMate, IPTV Smarters, VLC, Kodi, Plex or Jellyfin through an
M3U playlist...) talk to the proxy and never see the provider's address or
credentials. Use it to share IPTV at home without handing out the provider's
login, to put a provider behind your own domain, HTTPS or VPN, or to give
every device one address that does not change.

It is one binary or one container, set up with a few flags or environment
variables. There is no database and nothing to configure in a web interface.

## Quick start

In the examples, `192.168.1.10` is the machine running the proxy, as your
players reach it, and `family` / `choose-a-password` are the credentials you
give to your players. Replace them with your own.

### Docker, with an M3U playlist

```sh
docker run -d --name iptv-proxy -p 8080:8080 \
  -e M3U_URL="http://provider.example:8080/playlist.m3u" \
  -e HOSTNAME=192.168.1.10 \
  -e USER=family \
  -e PASSWORD=choose-a-password \
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
  -e HOSTNAME=192.168.1.10 \
  -e USER=family \
  -e PASSWORD=choose-a-password \
  -e GIN_MODE=release \
  pierro777/iptv-proxy:latest
```

### Docker, with an Xtream Codes account

```sh
docker run -d --name iptv-proxy -p 8080:8080 \
  -e XTREAM_BASE_URL="http://provider.example:8080" \
  -e XTREAM_USER=xtream_user \
  -e XTREAM_PASSWORD=xtream_password \
  -e HOSTNAME=192.168.1.10 \
  -e USER=family \
  -e PASSWORD=choose-a-password \
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

Tags: `latest`, and for a given version `v3.9.0`, `v3.9` or `v3`. On ARM
(Raspberry Pi, Apple silicon), add `-arm64`: `pierro777/iptv-proxy:latest-arm64`.

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
      HOSTNAME: 192.168.1.10
      # What your players log in with
      USER: family
      PASSWORD: choose-a-password
      GIN_MODE: release
```

```sh
docker compose up -d
```

The [`docker-compose.yml`](docker-compose.yml) of this repository does the
same, building the image from the sources.

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

Every option is a flag or an environment variable: the flag's name in upper
case, with `_` instead of `-`. A flag wins over the variable.

| Flag | Environment variable | Default | What it does |
|---|---|---|---|
| `--m3u-url`, `-u` | `M3U_URL` | | Provider playlist: an `http(s)` address or a local file. |
| `--xtream-base-url` | `XTREAM_BASE_URL` | | Provider's Xtream Codes address, e.g. `http://provider.example:8080`. |
| `--xtream-user` | `XTREAM_USER` | | Provider's Xtream Codes user. |
| `--xtream-password` | `XTREAM_PASSWORD` | | Provider's Xtream Codes password. |
| `--hostname` | `HOSTNAME` | | Name or IP your players reach the proxy at. It is written in every address the proxy gives out. |
| `--port` | `PORT` | `8080` | Port the proxy listens on. |
| `--advertised-port` | `ADVERTISED_PORT` | value of `--port` | Port written in the addresses the proxy gives out, when it differs from the listening port (behind a reverse proxy, or a different published Docker port). |
| `--https` | `HTTPS` | `false` | Write `https://` instead of `http://` in the addresses the proxy gives out. The proxy itself always listens in plain HTTP: put a reverse proxy in front for TLS. |
| `--user` | `USER` | `usertest` | User your players log in with. |
| `--password` | `PASSWORD` | `passwordtest` | Password your players log in with. |
| `--m3u-file-name` | `M3U_FILE_NAME` | `iptv.m3u` | Name of the playlist the proxy serves: `http://host:port/iptv.m3u`. |
| `--custom-endpoint` | `CUSTOM_ENDPOINT` | | Prefix put before every path: `http://host:port/<custom-endpoint>/iptv.m3u`. |
| `--custom-id` | `CUSTOM_ID` | random | First path element of M3U track addresses. Random at each start by default; set it to keep the same track addresses across restarts. |
| `--m3u-cache-expiration` | `M3U_CACHE_EXPIRATION` | `1` | Hours a playlist fetched from an Xtream provider is kept before asking for it again. |
| `--xtream-api-get` | `XTREAM_API_GET` | `false` | Build the `get.php` playlist (live channels) from the provider's API, for providers that disabled `get.php`. |
| `--user-agent` | `USER_AGENT` | | User-Agent sent to the provider instead of the player's. Some providers only answer known players. |
| `--iptv-proxy-config` | | | YAML file holding the same options, named as the flags (`m3u-url: ...`). |

Good to know:

- Change `--user` and `--password`: the defaults are public.
- `USER` and `HOSTNAME` are also ordinary system variables. When you run the
  binary from a shell, pass `--user` explicitly, or your login name is used. In
  Docker, always set `HOSTNAME`, or the container's ID is used.
- `GIN_MODE=release` is not an option of the proxy but of its web framework.
  Set it: without it, the proxy lists its routes at startup, and they contain
  your user and password. The access log masks them in both modes.

## M3U playlists

The proxy reads the provider's playlist once, at startup, and serves it at
`/iptv.m3u` with every track address replaced by its own. All other lines
(names, logos, groups, guide IDs, player options) are kept as the provider
wrote them. Restart the proxy to pick up a new version of the playlist.

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
http://192.168.1.10:8080/e3c0c308/family/choose-a-password/0/1.ts
#EXTINF:-1 tvg-id="sport.example" tvg-name="Sport" tvg-logo="http://provider.example/logos/sport.png" group-title="Sport",Sport HD
http://192.168.1.10:8080/e3c0c308/family/choose-a-password/1/2.m3u8
```

`e3c0c308` is the `--custom-id`. Tokens and other query parameters of the
provider's addresses stay on the proxy.

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
credentials.

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

## HLS

HLS streams (`.m3u8`) work in both modes, whatever the provider. Every address
an HLS playlist names (variants, segments, keys, audio tracks) is replaced by
an address of the proxy, `/hls/<token>/<name>`, so the whole stream goes
through the proxy, on any host and after any redirect. The token is the
provider's address, encrypted: a player never sees the provider's host,
credentials or session tokens. Nothing is stored, and tokens stay valid across
restarts.

## Behind a reverse proxy, with HTTPS

The proxy listens in plain HTTP. To serve it on your own domain with HTTPS,
put a reverse proxy (Traefik, Caddy, nginx...) in front, and tell iptv-proxy
which addresses to give out:

```yaml
    environment:
      PORT: 8080             # where iptv-proxy listens
      HOSTNAME: iptv.example.com
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
  come.
- It has one provider and one user and password, shared by all your players.
- It has no web interface.

## Roadmap

Planned, not available yet:

- Sharing one provider connection between several players watching the same channel.
- Filtering the channels and groups a playlist contains.
- Several providers behind one proxy.
- Several users, each with their own credentials.

See the [changelog](CHANGELOG.md) for what each release brought.

## License

[GPL-3.0](LICENSE).

Built with [cobra](https://github.com/spf13/cobra) and
[gin](https://github.com/gin-gonic/gin).

If the project is useful to you, you can buy me a beer:

[![paypal](https://www.paypalobjects.com/en_US/i/btn/btn_donate_LG.gif)](https://www.paypal.com/donate?hosted_button_id=WQAAMQWJPKHUN)
