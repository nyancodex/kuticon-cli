// Package version is the identity of one kuticon CLI build.
// Release builds set Version and Build with -ldflags -X; a plain
// go build or go install reports dev+0 (go install also embeds module info).
package version

import "runtime/debug"

var (
	Version = "dev"
	Build   = "0"
)

func init() {
	if Version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		Version = bi.Main.Version
	}
}

// String is Version+"+"+Build, or just Version for an unstamped build
// that took its version from the module (go install or go build).
func String() string {
	if Build == "0" && Version != "dev" {
		return Version
	}
	return Version + "+" + Build
}
