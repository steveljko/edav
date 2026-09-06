FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO stays off so the result is a static binary with nothing to link against
# in the final stage. modernc.org/sqlite is pure Go, so this costs nothing.
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/dav ./cmd/dav

# The data directory is created here so that a fresh named volume inherits its
# ownership; Docker copies the image's ownership onto an empty volume, and a
# root-owned one would leave the unprivileged process unable to write.
#
# /tmp comes along too. SQLite spills large sorts and its rollback journal to
# the system temp directory, and a scratch image has no directories at all.
RUN mkdir -p /out/data /out/tmp \
    && chown -R 65532:65532 /out/data /out/tmp \
    && chmod 1777 /out/tmp


FROM scratch

COPY --from=build /out/dav /dav
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/tmp /tmp

# No CA certificates: the server makes no outbound TLS connections. No tzdata
# either, because the binary embeds it, which CalDAV needs to resolve TZID.
USER 65532:65532
WORKDIR /data
VOLUME /data

ENV EDAV_ADDR=:8080 \
    EDAV_DB_PATH=/data/edav.db

EXPOSE 8080

# scratch has no shell, so the binary probes itself.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/dav", "-healthcheck"]

ENTRYPOINT ["/dav"]
