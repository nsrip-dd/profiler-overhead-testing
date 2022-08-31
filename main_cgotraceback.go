//go:build cgotraceback
// +build cgotraceback

package main

import (
	_ "github.com/nsrip-dd/cgotraceback"
)

func init() {
	usingCgotraceback = true
}
