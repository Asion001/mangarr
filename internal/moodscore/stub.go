//go:build !onnx

package moodscore

import (
	"context"
	"fmt"
)

// Built reports whether this build can run the model at all.
const Built = false

// Available reports whether pages can be scored here, and why not.
func Available() (bool, string) {
	return false, "this build of mangarr has no ONNX Runtime support (the full image and the worker downloads do)"
}

// New always fails in this build.
func New(context.Context, Config) (Scorer, error) {
	_, why := Available()
	return nil, fmt.Errorf("%w: %s", ErrUnavailable, why)
}
