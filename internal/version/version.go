package version

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// Print prints the program’s version info to stdout.
// It returns an error only if it can’t read the build info.
func Print() error {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return fmt.Errorf("no build info available")
	}

	// Module version, e.g. "v1.2.3" or "v0.0.0-20250806123456-abcd1234"
	// Go toolchain will fill this in automatically when building a module.
	version := strings.TrimPrefix(buildInfo.Main.Version, "v")

	// The Go version used to build
	goVersion := buildInfo.GoVersion

	// Look for VCS settings (commit and time)
	var (
		revision  = "unknown"
		buildTime = "unknown"
	)
	for _, s := range buildInfo.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 8 {
				revision = s.Value[:8]
			} else {
				revision = s.Value
			}
		case "vcs.time":
			buildTime = s.Value
		}
	}

	prog := filepath.Base(os.Args[0])
	fmt.Printf(
		"Version: %s version %s (built with %s, commit %s on %s)\n",
		prog, version, goVersion, revision, buildTime,
	)
	return nil
}
