// Package version is set at build time via -ldflags.
package version

var (
	Version = "dev"
	Build   = "local"
	Commit  = "unknown"
	Date    = ""

	// UpdateURL is where the worker zips of this build are published, as a
	// template: {version}, {build}, {commit} and {zip} (the zip's file name)
	// are filled in. CI sets it (UPDATE_URL); empty, workers are told about
	// a newer server but can't fetch it.
	UpdateURL = ""
	// Image is the container image this build is published as, without a
	// tag (IMAGE; empty when unknown).
	Image = ""
)
