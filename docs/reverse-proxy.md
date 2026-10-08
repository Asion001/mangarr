# Reverse proxy

mangarr listens on two ports:

- `8787` (`MANGARR_LISTEN`): the web UI and the API.
- `25600` (`MANGARR_KOMGA_LISTEN`): the Komga-compatible API, OPDS and
  KOReader sync for reading apps. It only listens while Settings → Reading
  apps → *Allow Komga apps to connect* is on.

Let the proxy terminate TLS. Browsers only speak HTTP/2 over HTTPS, and
over HTTP/1.1 the live-updates stream (`/api/v1/events`, Server-Sent
Events) holds one of the six connections a browser opens per site, which
the reader feels when it loads pages. mangarr speaks HTTP/1.1 and
cleartext HTTP/2 (h2c) to the proxy, so either works.

The events stream must not be buffered. mangarr sends
`X-Accel-Buffering: no`, which nginx honours; the examples below also turn
buffering off for it explicitly.

After the proxy works, set Settings → General → *Public URL* to the address
people use (e.g. `https://manga.example.com`). Links in notifications, single
sign-on callbacks and the reading-app guides are built from it. For the
reading-app port, set Settings → Reading apps → *Address apps should use*.

## Sub path (`MANGARR_URL_BASE`)

To serve mangarr under a path, e.g. `https://example.com/mangarr`, set
`MANGARR_URL_BASE=/mangarr` and forward the path **as is**: don't strip the
prefix in the proxy. The reading-app API doesn't use the sub path; give it
its own host name or port.

## Caddy

```caddyfile
manga.example.com {
	reverse_proxy mangarr:8787 {
		# the live-updates stream
		flush_interval -1
	}
}

# reading apps (Komga API, OPDS, KOReader sync)
manga-apps.example.com {
	reverse_proxy mangarr:25600
}
```

Under a sub path, with `MANGARR_URL_BASE=/mangarr`:

```caddyfile
example.com {
	handle /mangarr* {
		reverse_proxy mangarr:8787 {
			flush_interval -1
		}
	}
}
```

## nginx

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name manga.example.com;
    # ssl_certificate …; ssl_certificate_key …;

    client_max_body_size 0;          # backup uploads and imports can be large

    location / {
        proxy_pass http://mangarr:8787;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /api/v1/events {
        proxy_pass http://mangarr:8787;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_read_timeout 1h;
    }
}

server {
    listen 443 ssl;
    http2 on;
    server_name manga-apps.example.com;

    location / {
        proxy_pass http://mangarr:25600;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Under a sub path, use `location /mangarr/ { proxy_pass http://mangarr:8787; … }`
and `location /mangarr/api/v1/events { … }` (no URI after the upstream
address, so the path is passed unchanged).

## Traefik (Docker labels)

```yaml
services:
  mangarr:
    image: ghcr.io/asion001/mangarr:latest
    labels:
      - traefik.enable=true
      - traefik.http.routers.mangarr.rule=Host(`manga.example.com`)
      - traefik.http.routers.mangarr.entrypoints=websecure
      - traefik.http.routers.mangarr.tls.certresolver=letsencrypt
      - traefik.http.routers.mangarr.service=mangarr
      - traefik.http.services.mangarr.loadbalancer.server.port=8787
      # reading apps
      - traefik.http.routers.mangarr-apps.rule=Host(`manga-apps.example.com`)
      - traefik.http.routers.mangarr-apps.entrypoints=websecure
      - traefik.http.routers.mangarr-apps.tls.certresolver=letsencrypt
      - traefik.http.routers.mangarr-apps.service=mangarr-apps
      - traefik.http.services.mangarr-apps.loadbalancer.server.port=25600
```

Traefik streams Server-Sent Events without extra settings. Under a sub
path, use ``PathPrefix(`/mangarr`)`` in the rule with `MANGARR_URL_BASE=/mangarr`
and no `StripPrefix` middleware.

## Behind an auth proxy

With Authelia, Authentik or another forward-auth proxy in front, you can
turn mangarr's own login off with `MANGARR_AUTH_DISABLED=true`. Everyone who
gets through the proxy is then an admin, so only do this when the proxy
guards every route, including `/api`. Single sign-on (OpenID Connect,
`docs/setup.md` §9) keeps mangarr's accounts and permissions instead.
Reading apps can't get through a forward-auth login page, so keep the
reading-app host outside it.

## Behind a CDN or caching proxy

Every response says how it may be cached, so a CDN that honours
`Cache-Control` needs no rules of its own:

| What | Cache-Control | Shared cache |
| --- | --- | --- |
| `assets/*` (hashed build files) | `public, max-age=31536000, immutable` | yes, for good |
| `index.html` and other UI files | `no-cache` | revalidates each time |
| Discover thumbnails (signed, `/api/v1/discover/thumbnail`) | `public, max-age=…` until the link expires | yes |
| Covers, thumbnails, page images, previews | `private, max-age=…` with an `ETag` | never |
| Everything else under `/api/` | `private, no-store` | never |
| `/api/v1/events` (live updates) | `private, no-store, no-transform` | never, and must not be buffered |

Anything behind a login is `private`, so a cache in front of mangarr never
hands one person's library to someone else. If your CDN has a "cache
everything" mode, keep `/api/` out of it, or set it to respect the
origin's headers.

### Letting the CDN cache images

Covers and pages need a login, so by default a CDN passes every one of
them through to mangarr. To have the CDN keep them, turn on **Let a CDN
cache covers and pages** (Settings › Reading apps). Covers and downloaded
pages then get signed links under `/api/v1/img/`:

- a page link names the file it comes from and is `public, max-age=31536000,
  immutable`, since a new file gets a new link;
- a cover link is `public, max-age=86400` with an `ETag`, since a cover can
  change under the same link.

The signature is the permission: mangarr only hands a link to someone who
can see the image, but anyone holding the link can open it without logging
in. Links stop working after about two months (the reader gets fresh ones
as it opens chapters), or at once with **Reset image links**. Pages that
are streamed from a source rather than downloaded keep their private links.
