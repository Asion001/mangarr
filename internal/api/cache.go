package api

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/reading"
)

// Cache-Control values. Anything behind a login is private: the browser may
// keep it, but a CDN or shared proxy in front of the server must not, or it
// would hand one person's library to the next request for the same URL.
const (
	// cacheForever is for content-hashed build assets, the same for everyone.
	cacheForever = "public, max-age=31536000, immutable"
	// cacheNever is the default for /api/: answers that depend on who asks
	// and change any time.
	cacheNever = "private, no-store"
	// cacheRecheck makes the browser revalidate every time (index.html).
	cacheRecheck = "no-cache"
	// cacheSignedCover is for a signed cover link: a cover can change under
	// the same link (a new cover.jpg), so a cache keeps it a day and then
	// asks again with its ETag.
	cacheSignedCover = "public, max-age=86400, stale-while-revalidate=604800"
	// cacheStream is for server-sent events: nothing may store, compress or
	// buffer them on the way.
	cacheStream = "private, no-store, no-transform"
)

// cachePrivate lets the browser keep a response for d.
func cachePrivate(d time.Duration) string {
	return "private, max-age=" + strconv.Itoa(int(d.Seconds()))
}

type ifNoneMatchKey struct{}

// apiCacheDefaults marks every /api/ response private, no-store unless its
// operation says otherwise, and keeps the request's If-None-Match so image
// operations can answer 304.
func apiCacheDefaults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", cacheNever)
			if inm := r.Header.Get("If-None-Match"); inm != "" {
				r = r.WithContext(context.WithValue(r.Context(), ifNoneMatchKey{}, inm))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// imageReply sends image bytes with an ETag taken from them, or a 304 when
// the browser already has the same bytes.
func imageReply(ctx context.Context, data []byte, contentType, cacheControl string) *imageOutput {
	h := fnv.New64a()
	_, _ = h.Write(data)
	etag := fmt.Sprintf(`"%x"`, h.Sum64())
	out := &imageOutput{Status: http.StatusOK, ContentType: contentType, CacheControl: cacheControl, ETag: etag, Body: data}
	if inm, _ := ctx.Value(ifNoneMatchKey{}).(string); reading.ETagMatches(inm, etag) {
		out.Status, out.Body = http.StatusNotModified, nil
	}
	return out
}

// coverCache is how long a browser keeps a cover before asking again (the
// ETag makes asking cheap): a day when the URL carries the series' version,
// an hour otherwise. The file can change without the series changing, so
// not for good.
func coverCache(version string) string {
	if version != "" {
		return cachePrivate(24 * time.Hour)
	}
	return cachePrivate(time.Hour)
}
