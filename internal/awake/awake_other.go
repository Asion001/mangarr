//go:build !windows && !darwin && !linux

package awake

import "errors"

func hold() (func(), error) { return nil, errors.New("keeping awake isn't supported here") }
