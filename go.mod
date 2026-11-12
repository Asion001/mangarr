module github.com/Asion001/mangarr

go 1.27.1

require (
	github.com/PuerkitoBio/goquery v1.13.0
	github.com/coreos/go-oidc/v3 v3.21.0
	github.com/danielgtaylor/huma/v2 v2.40.0
	github.com/gen2brain/avif v0.6.0
	github.com/gen2brain/webp v0.6.4
	github.com/go-chi/chi/v5 v5.5.0
	github.com/pressly/goose/v3 v3.29.0
	github.com/shota3506/onnxruntime-purego v0.0.0-20260315223538-8db8bd7424b2
	github.com/uptrace/bun v1.3.0
	github.com/uptrace/bun/dialect/pgdialect v1.3.0
	github.com/uptrace/bun/dialect/sqlitedialect v1.3.0
	github.com/uptrace/bun/driver/pgdriver v1.3.0
	github.com/vektah/gqlparser/v2 v2.5.64
	golang.org/x/crypto v0.58.0
	golang.org/x/image v0.48.0
	golang.org/x/net v0.62.0
	golang.org/x/oauth2 v0.38.0
	golang.org/x/sync v0.25.0
	golang.org/x/sys v0.50.0
	golang.org/x/text v0.44.0
	howett.net/plist v1.0.1
	modernc.org/sqlite v1.62.0
)

require (
	github.com/agnivade/levenshtein v1.2.2 // indirect
	github.com/andybalholm/cascadia v1.3.4 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/ebitengine/purego v0.10.1 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/sethvargo/go-retry v0.5.0 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/tmthrgd/go-hex v0.0.0-20190904060850-447a3041c3bc // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	mellium.im/sasl v0.3.2 // indirect
	modernc.org/libc v1.80.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/shota3506/onnxruntime-purego => ./third_party/onnxruntime-purego
