# Filet

A minimalistic, fast, self-hosted web file manager. One static Go binary
embeds a SvelteKit app; state lives in one SQLite file; one user signs in with
credentials from the environment and gets access to the directories
("places") listed in a YAML config.

- Browse in list or grid view (remembered per directory), sort, search by
  name, preview almost anything (images incl. HEIC/RAW renditions, video and
  audio with on-the-fly remuxing, text/code, large logs, Markdown, PDF, CSV,
  fonts, zip/tar/7z archives).
- Upload files and folders with **resumable, BLAKE3-verified, parallel
  chunked uploads** that survive server crashes, closed tabs and network
  outages.
- Copy, move, rename, delete to a per-place trash, restore — as background
  jobs with progress, cancel and overwrite / keep both / skip prompts.
- Expiring, optionally password-protected share links; short-lived links for
  external players; drive health (SMART) and free space per place.
- Installable PWA with a Web Share Target.

## Quick start (Docker)

```yaml
# compose.yaml
services:
  filet:
    image: filet:latest
    restart: unless-stopped
    ports: ["127.0.0.1:8080:8080"]
    environment:
      FILET_USERNAME: admin
      FILET_PASSWORD: ${FILET_PASSWORD}
    volumes:
      - ./config:/config:ro
      - ./data:/data
      - /srv/media:/places/media
    # Optional SMART support:
    # user: "0:0"
    # devices: ["/dev/sda", "/dev/nvme0n1"]
    # cap_add: ["SYS_RAWIO", "SYS_ADMIN"]
```

`config/config.yaml`:

```yaml
listen: ":8080"
database: /data/filet.db
places:
  - name: Media
    path: /places/media
```

Build the image with `make docker`. It contains ffmpeg, libvips (with HEIF
support) and smartmontools.

## Bare metal

```sh
make build                       # → bin/filet (static, CGO_ENABLED=0)
sudo install bin/filet /usr/local/bin/
sudo cp deploy/filet.service /etc/systemd/system/
sudo cp deploy/config.example.yaml /etc/filet/config.yaml   # edit places
sudo cp deploy/filet.env.example /etc/filet/filet.env && sudo chmod 600 /etc/filet/filet.env
sudo systemctl enable --now filet
```

Install the optional tools from your distribution:
`apt install ffmpeg libvips-tools libheif-plugin-libde265 libheif-plugin-dav1d smartmontools`.
A missing tool only disables the features that need it.

## Configuration

`filet --config /etc/filet/config.yaml` (`--version` prints the version).
Every key except `database` and `places` has a safe default; see
[`deploy/config.example.yaml`](deploy/config.example.yaml) for all of them.
Unknown keys are an error. Credentials come only from the environment:

| Variable | Meaning |
|---|---|
| `FILET_USERNAME` | The login username (required) |
| `FILET_PASSWORD` | The login password (required; changing it signs everyone out at the next start) |

Exit codes: `0` clean shutdown, `1` config/environment error, `2` usage
error, `3` runtime error.

## Reverse proxy

Filet speaks plain HTTP; terminate TLS in a proxy. The proxy must forward the
`Host` header (or list the public origin in `auth.trusted_origins`), allow
request bodies of at least 64 MiB, must not buffer requests or responses, and
should use long timeouts. HTTP/2 to browsers is strongly recommended.

Caddy: see [`deploy/Caddyfile.example`](deploy/Caddyfile.example). nginx:

```nginx
server {
    listen 443 ssl http2;
    server_name files.example.com;
    # ssl_certificate ...;

    client_max_body_size 64m;
    proxy_request_buffering off;
    proxy_buffering off;
    proxy_read_timeout 1h;
    proxy_send_timeout 1h;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

## SMART health

smartctl needs read access to the device nodes plus `CAP_SYS_RAWIO`
(SATA/SAS) and `CAP_SYS_ADMIN` (NVMe). With systemd add
`AmbientCapabilities=CAP_SYS_RAWIO CAP_SYS_ADMIN` and
`SupplementaryGroups=disk`; with Docker pass the devices, add the
capabilities and run as root (see the compose example). Sleeping drives are
never woken (`--nocheck=standby`). Places on ZFS, NFS, tmpfs or overlay need
`smart_devices` in the config.

## Development

Requirements: Go 1.26, Node 24 with pnpm 10; ffmpeg/libvips are optional.

```sh
make dev     # run the filet:dev Docker image on 127.0.0.1:8080 (login test/test)
make test    # go test (with -tags filettest), vitest, svelte-check
make e2e     # Playwright against bin/filet-test (built with -tags filettest)
make lint    # golangci-lint + svelte-check
make sqlc    # regenerate internal/db/gen after editing queries
```

`make dev` runs the `filet:dev` image (build it with `make docker VERSION=dev`)
with `deploy/config.dev.yaml`, mounting `.dev/data` and `.dev/files` from the
repository root (created if missing).

The `filettest` build tag compiles in fault injection for tests
(`X-Filet-Test-Corrupt-Chunk`, `X-Filet-Test-Delay-Ms`,
`X-Filet-Test-Drop-After-Bytes`) and `/__test/*` endpoints (clock, fake free
space, request log, copy delay). Release builds contain none of it.

Layout: `backend/` (Go module `filet`: `cmd/filet`, `internal/…`),
`frontend/` (SvelteKit SPA, built into `backend/internal/web/dist` by
`make frontend`), `deploy/`.

## Known limitations

- Linux only for production (fallocate, renameat2, statfs, `/sys`,
  `/proc/self/mountinfo`). The code builds on macOS for development with
  simplified fallbacks.
- Partial upload files whose database rows were lost (for example a restored
  older database) are not searched for; they stay hidden as reserved names.
- Changes made outside Filet appear on navigation, refresh or window focus;
  there is no filesystem watching.
- Thumbnail cache entries for changed files are not garbage-collected.
- iOS/iPadOS Safari does not support Web Share Target (installing still
  works).
- `.ts` files are classified as MPEG transport streams; TypeScript files with
  that extension still open as text through the content sniff.
