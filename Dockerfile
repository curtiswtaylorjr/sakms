# syntax=docker/dockerfile:1.7

FROM golang:1.25-trixie AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/tidyarr ./cmd/tidyarr

# Debian, not Alpine: ffmpeg's Debian package is the more predictable ffprobe
# build, and CGO is off anyway (modernc.org/sqlite is pure Go), so there's no
# musl-vs-glibc tradeoff to weigh here.
FROM debian:trixie-slim
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg gosu \
    && useradd --create-home --home-dir /data --uid 1000 tidyarr

COPY --from=build /out/tidyarr /usr/local/bin/tidyarr
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

ENV TIDYARR_ADDR=:8080 \
    TIDYARR_DATA_DIR=/data

VOLUME /data
EXPOSE 8080
# Stays root here so the entrypoint can chown a bind-mounted /data before
# dropping to the unprivileged tidyarr user via gosu — see
# docker-entrypoint.sh for why.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/tidyarr"]
