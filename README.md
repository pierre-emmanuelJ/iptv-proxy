# Iptv Proxy

## Description

### M3u

Iptv Proxy is a project to convert an iptv m3u file

into a web proxy server And give a new m3u file

with the new routes to the proxy server

### Xtream server api

proxy on xtream server api

support live, vod, series and full epg :rocket:

### M3u Example

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

What m3u proxy IPTV do:
 - convert chanels url to new endpoints
 - convert original m3u file with new routes

start proxy server example:
```Bash
iptv-proxy --m3u-url http://example.com/get.php?username=user&password=pass&type=m3u_plus&output=m3u8 \
             --port 8080 \
             --hostname poxyexample.com \
             ##### UNSAFE AUTH TODO ADD REAL AUTH
             --user test \
             --password passwordtest
```


 that's give you the m3u file on a specific endpoint:
 
 `http://poxyserver.com:8080/iptv.m3u?username=test&password=passwordtest`

```m3u
#EXTM3U
#EXTINF:-1 tvg-ID="examplechanel1.com" tvg-name="chanel1" tvg-logo="http://ch.xyz/logo1.png" group-title="USA HD",CHANEL1-HD
http://poxyserver.com:8080/12/test/1?username=test&password=passwordtest
#EXTINF:-1 tvg-ID="examplechanel2.com" tvg-name="chanel2" tvg-logo="http://ch.xyz/logo2.png" group-title="USA HD",CHANEL2-HD
http://poxyserver.com:8080/13/test/2?username=test&password=passwordtest
#EXTINF:-1 tvg-ID="examplechanel3.com" tvg-name="chanel3" tvg-logo="http://ch.xyz/logo3.png" group-title="USA HD",CHANEL3-HD
http://poxyserver.com:8080/14/test/3?username=test&password=passwordtest
#EXTINF:-1 tvg-ID="examplechanel4.com" tvg-name="chanel4" tvg-logo="http://ch.xyz/logo4.png" group-title="USA HD",CHANEL4-HD
http://poxyserver.com:8080/15/test/4?username=test&password=passwordtest
```
### Xtream code server api Example

```Bash
% iptv-proxy --m3u-url http://example.com:1234/get.php?username=user&password=pass&type=m3u_plus&output=m3u8 \
             --port 8080 \
             --hostname poxyexample.com \
             ## put xtream flags if you want to add xtream proxy
             --xtream-user xtream_user \
             --xtream-password xtream_password \
             --xtream-base-url http://example.com:1234 \
             ##### UNSAFE AUTH TODO ADD REAL AUTH
             --user test \
             --password passwordtest
             
```

What xtream proxy do:
 - convert xtream `xtream-user ` and `xtream-password` into new `user` and `password`
 - convert `xtream-base-url` with `hostname` and `port`
 
 original xtream credentials:
 ```
 user: xtream_user
 password: xtream_password
 base-url: http://example.com:1234
 ```
 new xtream credentials:
 ```
 user: test
 password: passwordtest
 base-url: http://poxyexample.com:8080
 ```
 
 All xtream live, streams, vod, series... are poxyfied! 
 
 
 You can get the m3u file with the xtream api request:
 ```
 http://poxyexample.com:8080/get.php?username=test&password=passwordtest&type=m3u_plus&output=ts
 ```


## Installation

### Without Docker

Download lasted [release](https://github.com/pierre-emmanuelJ/iptv-proxy/releases)

Or

`% go install` in root repository

```Bash
% iptv-proxy --m3u-url http://example.com:1234/get.php?username=user&password=pass&type=m3u_plus&output=m3u8 \
             --port 8080 \
             --hostname poxyexample.com \
             ## put xtream flags if you want to add xtream proxy
             --xtream-user xtream_user \
             --xtream-password xtream_password \
             --xtream-base-url http://example.com:1234 \
             ##### UNSAFE AUTH TODO ADD REAL AUTH
             --user test \
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
      # M3U_URL: http://example.com:1234/get.php?username=user&password=pass&type=m3u_plus&output=m3u8
      M3U_URL: /root/iptv/iptv.m3u
      # Port to expose the IPTVs endpoints
      PORT: 8080
      # Hostname or IP to expose the IPTVs endpoints (for machine not for docker)
      HOSTNAME: localhost
      GIN_MODE: release
      ## Xtream-code proxy configuration
      ## (put these env variables if you want to add xtream proxy)
      XTREAM_USER: xtream_user
      XTREAM_PASSWORD: xtream_password
      XTREAM_BASE_URL: "http://example.com:1234"
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


