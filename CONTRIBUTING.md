# Contributing

Bug reports, ideas and pull requests are welcome. For a question, use the
[discussions](https://github.com/pierre-emmanuelJ/iptv-proxy/discussions).

## What the proxy aims for

A simple, reliable IPTV proxy with nothing to configure beyond a provider and
a password. A change that needs a new option should say why the proxy cannot
do the right thing by itself. The proxy never shows a provider's credentials
to clients, and never writes them in its logs: changes keep it so, with a
test.

## Building and testing

Go is the only requirement (the version in `go.mod`); ffmpeg is needed for the
tests of the HDHomeRun tuner's HLS channels, which are skipped without it.

```sh
go build ./...
go test -race ./...
```

Before a pull request, run what the CI runs:

```sh
gofmt -l .                      # prints nothing
golangci-lint run               # finds nothing
go test -race -count=1 ./...
go test -count=1 -cover ./...   # each package stays at 80% or more
go test ./pkg/m3u/ -run '^$' -fuzz FuzzParse -fuzztime 30s   # and the other fuzzers of .github/workflows/ci.yml
docker build .                  # and docker build --target ffmpeg .
```

The tests run the proxy against a fake provider (`newProvider` in
`pkg/server/server_test.go`): a change of behaviour comes with a test that
shows it there.

## Pull requests

- One change per pull request, with tests, and the README updated when users
  see a difference. The CHANGELOG is written at release.
- Code, comments and documentation are in English. Comments say why, in plain
  sentences.
- Keep the existing tests: a test that has to change says why in the pull
  request.

## Releases

Tags `vX.Y.Z` on `master` are released by GoReleaser: binaries, packages and
Docker images (the usual ones and the `-ffmpeg` ones).
