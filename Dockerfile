# Treasure Dash: one distroless image serving the UI, API and scheduler.

# ---- 1. static frontend -----------------------------------------------------------
FROM node:22-bookworm-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npx next build

# ---- 2. Go binary with the frontend embedded ----------------------------------------
FROM golang:1.27-bookworm AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN rm -rf internal/server/dist
COPY --from=web /web/out ./internal/server/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/usr/local/bin/treasuredash .

# ---- 3. pdftotext and exactly the shared libraries it links against -----------------
# Bank statements are read with poppler's pdftotext; distroless has no package
# manager, so the binary and its libraries are copied over from Debian 12 (the
# same release the distroless base is built on). Library paths are resolved
# through Debian's /lib → /usr/lib symlink so everything lands under /usr,
# saved under the soname the loader asks for.
FROM debian:12-slim AS poppler
RUN apt-get update && apt-get install -y --no-install-recommends poppler-utils && rm -rf /var/lib/apt/lists/*
RUN set -eu; mkdir -p /out/usr/bin /out/data; \
    cp /usr/bin/pdftotext /out/usr/bin/; \
    for lib in $(ldd /usr/bin/pdftotext | awk '/=>/ {print $3}'); do \
      real=$(realpath "$lib"); \
      case "$real" in /usr/*) ;; *) echo "unexpected library path $real"; exit 1 ;; esac; \
      mkdir -p "/out$(dirname "$real")"; cp "$real" "/out$(dirname "$real")/$(basename "$lib")"; \
    done; \
    mkdir -p /out/etc && cp -r /etc/fonts /out/etc/fonts

# ---- 4. runtime -----------------------------------------------------------------
FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=poppler /out/usr /usr
COPY --from=poppler /out/etc/fonts /etc/fonts
COPY --from=poppler --chown=65532:65532 /out/data /data
COPY --from=api /out/usr/local/bin/treasuredash /usr/local/bin/treasuredash
# DATABASE_URL (PostgreSQL) is supplied at runtime; without it the app uses
# SQLite in /data. Uploaded bills always live in /data.
ENV DATA_DIR=/data ADDR=:8080 TZ=Asia/Kolkata
VOLUME /data
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/treasuredash"]
