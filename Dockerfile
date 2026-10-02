FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /iptv-proxy .

FROM alpine:3
RUN apk add --no-cache ca-certificates
COPY --from=build /iptv-proxy /
ENTRYPOINT ["/iptv-proxy"]
