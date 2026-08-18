FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/notifyd ./cmd/notifyd && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockvendor ./cmd/mockvendor

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app
COPY --from=build /out/notifyd /usr/local/bin/notifyd
COPY --from=build /out/mockvendor /usr/local/bin/mockvendor
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/notifyd"]
