// Package workerupdate moves a remote worker onto the server's build. The
// server compares the build a worker reports with its own and, when its own
// is later, offers it on hello and on every lease. A desktop worker then
// stops taking work, finishes what it holds, downloads the worker zip of
// that build for its platform, checks it against the published checksum,
// swaps its own program and starts again. A worker in a container only says that an
// update is waiting: its image is replaced from outside (docs/setup.md).
package workerupdate

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Zipped are the platforms CI builds a worker zip for
// (scripts/worker-zip.sh, the worker-zips job).
var Zipped = []string{"windows/amd64", "darwin/arm64", "linux/amd64"}

// Build names one build of mangarr: its version (a release tag or the
// branch it was built from), the CI run number and the commit.
type Build struct {
	Version string `json:"version"`
	Build   string `json:"build,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

// String is the build as people see it: "v1.3.0 build 412" or "main build 412".
func (b Build) String() string {
	if b.Build == "" {
		return b.Version
	}
	return b.Version + " build " + b.Build
}

// run is the CI run number a build was made by (0: not made by CI).
func (b Build) run() int {
	n, err := strconv.Atoi(b.Build)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// Later reports whether server is a later build than worker, so worker
// should move to it. Two CI builds compare by run number: every push gets
// a higher one, whether it is a release or a branch. A worker that doesn't
// say its run number (an older one) compares by release version, and a
// local build on either side is never moved.
func Later(server, worker Build) bool {
	if s, w := server.run(), worker.run(); s > 0 && w > 0 {
		return s > w && server.Commit != worker.Commit
	}
	return worker.Build == "" && server.run() > 0 && Newer(server.Version, worker.Version)
}

// Offer is what the server tells a worker that should move to its build.
type Offer struct {
	Build
	// Image is the container image of that build (empty when the server
	// doesn't know where it is published).
	Image string `json:"image,omitempty"`
	// URL is the worker zip for the worker's platform; empty when this
	// build publishes none for it, and then it can only be updated by hand.
	URL string `json:"url,omitempty"`
	// Checksum is where the zip's SHA-256 is published.
	Checksum string `json:"checksumUrl,omitempty"`
}

// ID names the build offered, the way a skipped update is remembered.
func (o Offer) ID() string { return o.Build.String() }

// For is the update a worker on platform (GOOS/GOARCH) running build
// worker should get from a server running build server, or nil when it
// should stay as it is. urlTemplate and image say where the server's build
// is published (version.UpdateURL, version.Image).
func For(server, worker Build, platform, urlTemplate, image string) *Offer {
	if !Later(server, worker) {
		return nil
	}
	o := &Offer{Build: server}
	if image != "" {
		o.Image = image + ":" + strings.TrimPrefix(server.Version, "v")
	}
	if urlTemplate != "" && slices.Contains(Zipped, platform) {
		o.URL = strings.NewReplacer("{version}", server.Version, "{build}", server.Build, "{commit}", server.Commit,
			"{zip}", ZipName(platform)).Replace(urlTemplate)
		o.Checksum = o.URL + ".sha256"
	}
	return o
}

// ZipName is the worker zip of a platform (GOOS/GOARCH).
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
