// Package workerupdate moves a remote worker onto the server's version.
// The server compares the version a worker reports with its own and, when
// it is newer, offers it on hello and on every lease. A desktop worker then
// stops taking work, finishes what it holds, downloads the release zip for
// its platform, checks it against the published checksum, swaps its own
// program and starts again. A worker in a container only says that an
// update is waiting: its image is replaced from outside (docs/setup.md).
package workerupdate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Releases is where the release zips are published, one folder per tag.
const Releases = "https://github.com/Asion001/mangarr/releases/download"

// Image is the container image a worker in Docker runs.
const Image = "ghcr.io/asion001/mangarr"

// Zipped are the platforms a release publishes a worker zip for
// (scripts/worker-zip.sh, the worker-zips job in CI).
var Zipped = []string{"windows/amd64", "darwin/arm64", "linux/amd64"}

// Offer is what the server tells a worker that should move to its version.
type Offer struct {
	// Version is the server's own version, the one to move to.
	Version string `json:"version"`
	// Image is the container image of that version.
	Image string `json:"image"`
	// URL is the worker zip for the worker's platform; empty when releases
	// have none for it, and then it can only be updated by hand.
	URL string `json:"url,omitempty"`
	// Checksum is where the zip's SHA-256 is published.
	Checksum string `json:"checksumUrl,omitempty"`
}

// For is the update a worker on platform (GOOS/GOARCH) running
// workerVersion should get from a server running serverVersion, or nil when
// it should stay as it is: either side is not a release build, or the
// worker is as new as the server or newer.
func For(serverVersion, workerVersion, platform string) *Offer {
	if !Newer(serverVersion, workerVersion) {
		return nil
	}
	o := &Offer{Version: serverVersion, Image: Image + ":" + strings.TrimPrefix(serverVersion, "v")}
	for _, p := range Zipped {
		if p == platform {
			name := ZipName(platform)
			o.URL = fmt.Sprintf("%s/%s/%s", Releases, serverVersion, name)
			o.Checksum = o.URL + ".sha256"
		}
	}
	return o
}

// ZipName is the release zip of a platform (GOOS/GOARCH).
func ZipName(platform string) string {
	return "mangarr-worker-" + strings.ReplaceAll(platform, "/", "-") + ".zip"
}

// release matches the versions releases are tagged with: v1.2.3, or a
// pre-release such as v1.3.0-rc.1. What `git describe` makes of a commit
// after a tag (v1.2.3-4-gabc1234) is not one, and neither is a branch name.
var release = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.]+))?$`)

type parsed struct {
	nums [3]int
	pre  string
}

func parse(v string) (parsed, bool) {
	m := release.FindStringSubmatch(v)
	if m == nil || m[4] == "dirty" {
		return parsed{}, false
	}
	var p parsed
	for i := range 3 {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return parsed{}, false
		}
		p.nums[i] = n
	}
	p.pre = m[4]
	return p, true
}

// IsRelease reports whether v is a tagged release version.
func IsRelease(v string) bool {
	_, ok := parse(v)
	return ok
}

// Newer reports whether target is a later release than current. Anything
// that isn't a release version on either side is never newer: a build from
// a branch or a local checkout is left alone, and nothing moves backwards.
func Newer(target, current string) bool {
	t, ok := parse(target)
	if !ok {
		return false
	}
	c, ok := parse(current)
	if !ok {
		return false
	}
	for i := range 3 {
		if t.nums[i] != c.nums[i] {
			return t.nums[i] > c.nums[i]
		}
	}
	switch {
	case t.pre == c.pre:
		return false
	case t.pre == "": // the release after its pre-releases
		return true
	case c.pre == "":
		return false
	}
	return comparePre(t.pre, c.pre) > 0
}

// comparePre orders pre-release labels as semver does: dot-separated
// parts, numbers numerically and below words, fewer parts first.
func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				if an > bn {
					return 1
				}
				return -1
			}
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return len(as) - len(bs)
}
