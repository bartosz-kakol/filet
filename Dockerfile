# syntax=docker/dockerfile:1

FROM node:24-trixie-slim AS frontend
WORKDIR /src/frontend
RUN corepack enable && corepack prepare pnpm@10.18.0 --activate
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build

FROM golang:1.26-trixie AS backend
ARG VERSION=dev
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /src/frontend/build ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/filet ./cmd/filet

FROM debian:trixie-slim AS runtime
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ffmpeg libvips-tools libheif-plugin-libde265 libheif-plugin-dav1d \
		smartmontools ca-certificates tzdata \
	&& rm -rf /var/lib/apt/lists/*
COPY --from=backend /out/filet /usr/local/bin/filet
RUN groupadd -g 1000 filet && useradd -u 1000 -g 1000 -M -s /usr/sbin/nologin filet \
	&& mkdir -p /config /data && chown 1000:1000 /data
USER 1000:1000
VOLUME ["/config", "/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/filet"]
CMD ["--config", "/config/config.yaml"]
