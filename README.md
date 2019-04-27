# Iptv Proxy

## Description

### M3u

Iptv Proxy is a project to convert an iptv m3u file

into a web proxy server And give a new m3u file

with the new routes to the proxy server

### Xtream server api

proxy on xtream server api

support live, vod, series and full epg :rocket:

### Example

original iptv m3u file
```m3u
#EXTM3U
#EXTINF:-1 tvg-ID="examplechanel1.com" tvg-name="chanel1" tvg-logo="http://ch.xyz/logo1.png" group-title="USA HD",CHANEL1-HD
http://iptvexample.net:1234/12/test/1
#EXTINF:-1 tvg-ID="examplechanel2.com" tvg-name="chanel2" tvg-logo="http://ch.xyz/logo2.png" group-title="USA HD",CHANEL2-HD
http://iptvexample.net:1234/13/test/2
#EXTINF:-1 tvg-ID="examplechanel3.com" tvg-name="chanel3" tvg-logo="http://ch.xyz/logo3.png" group-title="USA HD",CHANEL3-HD
http://iptvexample.net:1234/14/test/3
#EXTINF:-1 tvg-ID="examplechanel4.com" tvg-name="chanel4" tvg-logo="http://ch.xyz/logo4.png" group-title="USA HD",CHANEL4-HD
http://iptvexample.net:1234/15/test/4
```

What proxy IPTV do
 - convert chanels url to new endpoints
 - convert original m3u file with new routes

start proxy server example:
```Bash
poxy-server --m3u-url http://iptvexample.net/iptvm3ufile.m3u \ # or local m3u file
            --port 8080 \ # port you want to expose your proxy
            --hostname proxyserver.com # hostname of your machine running this proxy
            ##### UNSAFE AUTH TODO ADD REAL AUTH
            --user test
            --password passwordtest
```


 - give you the m3u file on a specific endpoint `http://poxyserver.com:8080/iptv.m3u`

```m3u
#EXTM3U
#EXTINF:-1 tvg-ID="examplechanel1.com" tvg-name="chanel1" tvg-logo="http://ch.xyz/logo1.png" group-title="USA HD",CHANEL1-HD
http://poxyserver.com:8080/12/test/1
#EXTINF:-1 tvg-ID="examplechanel2.com" tvg-name="chanel2" tvg-logo="http://ch.xyz/logo2.png" group-title="USA HD",CHANEL2-HD
http://poxyserver.com:8080/13/test/2
#EXTINF:-1 tvg-ID="examplechanel3.com" tvg-name="chanel3" tvg-logo="http://ch.xyz/logo3.png" group-title="USA HD",CHANEL3-HD
http://poxyserver.com:8080/14/test/3
#EXTINF:-1 tvg-ID="examplechanel4.com" tvg-name="chanel4" tvg-logo="http://ch.xyz/logo4.png" group-title="USA HD",CHANEL4-HD
http://poxyserver.com:8080/15/test/4
```

## Installation

### Without Docker

Download lasted [release](https://github.com/pierre-emmanuelJ/iptv-proxy/releases)
```Bash
% iptv-proxy --m3u-url http://example.com/iptv.m3u \
             --port 8080 --hostname poxyexample.com \
             ##### UNSAFE AUTH TODO ADD REAL AUTH
             --user test
             --password passwordtest
```
Or

```Bash
% go install
% iptv-proxy --m3u-url http://example.com/iptv.m3u \
             --port 8080 --hostname poxyexample.com \
             ##### UNSAFE AUTH TODO ADD REAL AUTH
             --user test
             --password passwordtest
```

### With Docker

#### Prerequisite

 - Add an m3u URL in `docker-compose.yml` or add local file in `iptv` folder
 - `HOSTNAME` and `PORT` to expose
 - Expose same container port as the `PORT` ENV variable 

```Yaml
 ports:
       # have to be the same as ENV variable PORT
      - 8080:8080
 environment:
      # if you are using m3u remote file
      # M3U_URL: https://example.com/iptvfile.m3u
      M3U_URL: /root/iptv/iptv.m3u
      # Port to expose the IPTVs endpoints
      PORT: 8080
      # Hostname or IP to expose the IPTVs endpoints (for machine not for docker)
      HOSTNAME: localhost
      ##### UNSAFE AUTH TODO ADD REAL AUTH
      USER: test
      PASSWORD: testpassword
```

#### Start

```
% docker-compose up -d
```

## TODO

there is unsafe auth just for testing.
change with real auth with database and user management
and auth with token

**ENJOY!**

## Powered by

- [cobra](https://github.com/spf13/cobra)
- [go.xtream-codes](https://github.com/tellytv/go.xtream-codes)
- [gin](https://github.com/gin-gonic/gin)


