FROM golang:1.16-alpine

RUN apk add ca-certificates

WORKDIR /Users/ahurtaud/projects/private/src/github.com/pierre-emmanuelJ/iptv-proxy
COPY . .
ENV GOPATH=/Users/ahurtaud/projects/private
RUN GO111MODULE=off CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o iptv-proxy .

FROM alpine:3
COPY --from=0  /Users/ahurtaud/projects/private/src/github.com/pierre-emmanuelJ/iptv-proxy /
ENTRYPOINT ["/iptv-proxy"]
