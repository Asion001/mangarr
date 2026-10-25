# Writing modules

Everything mangarr talks to lives behind a module interface in
`internal/modules/<kind>`. Implementations live in
`internal/modules/<kind>/<impl>` and are the **only** packages allowed to
talk to vendor APIs (`scripts/import-lint.sh` enforces that core code never
imports an implementation).

## Anatomy

```go
package mymodule

type Settings struct {
    URL    string `json:"url" label:"Server URL" type:"url" required:"true" order:"1"`
    APIKey string `json:"apiKey" label:"API key" secret:"true" order:"2"`
    Extra  bool   `json:"extra" label:"Something advanced" advanced:"true" order:"3"`
}

func (s *Settings) Validate() error { return nil } // optional

func init() {
    modules.Register(&modules.Implementation{
        Kind: modules.KindMetadata, Name: "mymodule", DisplayName: "My provider",
        Description: "Shown when adding the module.",
        Settings: func() any { return &Settings{URL: "https://example.org"} }, // defaults
        New: func(deps modules.Deps, s any) (modules.Instance, error) {
            return &Module{s: s.(*Settings), http: deps.HTTP}, nil
        },
    })
}
```

Then add a blank import to `internal/modules/all/all.go`.

- **Field tags** (`label`, `help`, `type`, `order`, `required`, `advanced`,
  `secret`, `placeholder`, `options:"v:Label,…"`) become the schema served at
  `GET /api/v1/modules/schema`; the UI renders the form from it. Types:
  text, password, number, bool, select, url, textarea, tags (`[]string`),
  keyvalue (`map[string]string`).
- Secrets are masked in API responses and kept when the client sends the mask.
- `Test(ctx)` powers the **Test** button and health checks (unless the module
  implements `modules.HealthChecker` for a cheaper probe).
- Implement `modules.Closer` to release resources when settings change.

## Kinds

| Kind | Required interface | Optional capabilities |
|---|---|---|
| `source` | `source.Module`: `Sources`, `Search`, `Manga`, `Pages`, `FetchPage` | `Latest` (latest/popular), `ExtensionManager`, `Preferences`, `Thumbnails`, `Assets`, `Maintainer`, `AutoUpdater`, `Fetchable` (hand a page to another machine) |
| `metadata` | `metadata.Module`: `Search`, `Get` | `ExternalLookup` (join by another provider's id) |
| `library` | `library.Module`: `Rescan` | `ProgressReader` (per-user credentials + progress) |
| `notify` | `notify.Module`: `Send` | — |
| `upscale` | `upscale.Module`: `Info`, `Upscale` | — |
| `mediaserver` | `mediaserver.Module`: `Find` (adaptation → web URL) | — |

### Fetchable: pages a worker can get

A module that can say how a page is fetched — its address and the headers the
site expects — implements `Fetchable`. The server resolves the page list (that
needs the module's session and its pacing) and a download worker makes those
requests itself. A module without it is always downloaded on the server, so
nothing is ever undownloadable.

### Sites of our own

`internal/sources/sourcekit` is the toolkit mangarr's own sites are written
against, and it deliberately imports nothing else from mangarr; `sites/` has
one file per site and imports only the toolkit (a test enforces both, so they
can move to a repository of their own). A site's id is
`sourcekit.KeiyoushiID(name, lang, version)` — the id Mihon gives the same
extension — which is what keeps a backup import linking to it.

### Source identity

A manga is identified by `(SourceID, URL)` — keep these **portable** (for
Tachiyomi sources: the Mihon source id and the path without domain) so
another engine can take over existing series via *reassign module*.
Engine-specific ids go into `EngineRef`, which the core caches but never
relies on. Report chapter numbers as the source gives them (`-1` when
unknown); the core parses names Mihon-style as a fallback.

### Metadata merging

Return provider-native ids in `ID` and cross ids in `ExternalIDs`
(`"anilist"`, `"mal"`, `"mangaupdates"`, …) so the aggregator can merge the
same series across providers. Scalars come from the highest-priority module
that has them; lists are unioned.

## Tests

- Contract tests use `httptest` servers (see `komga_test.go`,
  `kavita_test.go`, `notify_contract_test.go`).
- `internal/testutil/fakesource` and `fakelibrary` provide in-memory modules
  for pipeline tests (`internal/app/*_test.go`).
- Real-service tests go in `internal/integration` behind the `integration`
  build tag.

## Media servers

The `mediaserver` kind has `jellyfin` and `silo` implementations. Configure
instances through the existing admin-only module API with `url` and `apiKey`
settings. The URL is the server's web root, including any reverse-proxy path
prefix. Credentials belong in the API key field, never the URL. Keys are
masked in module responses, and masked updates retain the stored key. These
are shared admin targets, not per-reader accounts; only expose servers whose
catalog presence may be shared with mangarr readers.

For example, `POST /api/v1/modules` accepts:

```json
{
  "kind": "mediaserver",
  "implementation": "jellyfin",
  "name": "Screen room",
  "enabled": true,
  "settings": {
    "url": "https://media.example/screen",
    "apiKey": "replace-with-server-key"
  }
}
```

Use `POST /api/v1/modules/test` with the same body to test before saving, or
`POST /api/v1/modules/{id}/test` for a saved instance. Jellyfin tests the
protected `/System/Info` endpoint; Silo tests an authenticated catalog read.
Requests use the module manager's HTTP transport, which allows admin-defined
LAN targets. Redirects are rejected to avoid forwarding keys. Upstream
response bodies and transport details are omitted from errors because they
can contain credentials.

`GET /api/v1/series/{id}` adds `watchLinks` to each top-level adaptation:

```json
{
  "serverName": "Screen room",
  "kind": "jellyfin",
  "url": "https://media.example/screen/web/index.html#!/details?id=screen-item"
}
```

The field is an array, ordered by module priority, with at most one link per
enabled server. No match, an ambiguous match, or an unavailable server yields
no link for that server. Existing AniList/MAL `links` remain available. The
stored `metadata.adaptations` is unchanged. List responses have empty
`watchLinks` arrays and do not perform media-server lookups. These links open
item detail pages; the reader signs in on that server, which enforces its own
playback access.

- **Jellyfin:** reads paginated `/Items` with `ProviderIds`, restricted to
  movies and series, excluding virtual items and placeholders. It matches
  `AniList` or `MAL`/`MyAnimeList` IDs case-insensitively by provider name,
  before considering titles. A snapshot of up to 50,000 items permits ID
  matches even when the server uses a different title. A conflicting provider
  ID rules out a title match.
- **Silo:** uses `Authorization: Bearer <API key>` and the native
  `/api/v1/catalog` query API (`q`, `type`, `year_min`, `year_max`, `offset`,
  `limit`, `snapshot`, `has_more`). Use an unscoped API key for an account with
  access to the intended libraries; Silo's current scoped-key allowlist does
  not include catalog reads. No profile or playback token is needed for this
  account-level catalog lookup. The native catalog does **not** expose
  AniList/MAL IDs, so this implementation uses title/year fallback only, with
  a 10,000-result search limit. Deep links use `/item/{content_id}`.
- **Fallback:** exact title equality after Unicode NFKC normalization,
  lowercasing, and removing punctuation/whitespace, plus an equal, known
  year. Movies match movies; TV formats match series; OVA/ONA/special may
  match either. Multiple distinct matching items are left unlinked.
- **Caching and failures:** each module instance caches matches and misses for
  ten minutes and failures for thirty seconds. Jellyfin also shares a
  ten-minute catalog snapshot, and invalidates its match cache with that
  snapshot. Caches are bounded, concurrent loads are coalesced, and any module
  settings reload discards them. Pagination failures never cache a partial
  catalog as success. All servers share a five-second enrichment deadline
  per series detail request and run independently; errors leave metadata
  usable. Oversized result sets fail without returning a partial match.

The Silo contract was checked at
[`1aa1eb2`](https://github.com/Silo-Server/silo-server/tree/1aa1eb2d80d947349d4458169080d5d32c4aabe9):
[catalog handler](https://github.com/Silo-Server/silo-server/blob/1aa1eb2d80d947349d4458169080d5d32c4aabe9/internal/api/handlers/catalog.go),
[catalog parser](https://github.com/Silo-Server/silo-server/blob/1aa1eb2d80d947349d4458169080d5d32c4aabe9/internal/catalog/catalog_parser.go),
[item response and account access](https://github.com/Silo-Server/silo-server/blob/1aa1eb2d80d947349d4458169080d5d32c4aabe9/internal/api/handlers/items.go),
[API-key middleware](https://github.com/Silo-Server/silo-server/blob/1aa1eb2d80d947349d4458169080d5d32c4aabe9/internal/api/middleware/auth.go),
[scope allowlist](https://github.com/Silo-Server/silo-server/blob/1aa1eb2d80d947349d4458169080d5d32c4aabe9/internal/api/middleware/api_key_scopes.go),
and [web routes](https://github.com/Silo-Server/silo-server/blob/1aa1eb2d80d947349d4458169080d5d32c4aabe9/web/src/App.tsx).
No reverse manga exposure or series-page UI is included.
