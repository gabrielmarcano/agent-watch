// Package buildinfo formats a binary's version together with the commit it
// was built from.
//
// The version is the component's entry in VERSIONS at the repo root, stamped
// by the Makefile (or the plugin build) with -ldflags "-X main.version=…".
// The commit is the VCS stamp `go build` embeds on its own when it builds
// inside a git checkout, so no build script has to pass it.
package buildinfo

import "runtime/debug"

// revisionLen is how much of the commit hash is shown, as `git log --oneline`.
const revisionLen = 7

// String returns version plus the commit the running binary was built from:
//
//	0.3.0 (c8aa72e)            built from a clean checkout
//	0.3.0 (c8aa72e, modified)  built with uncommitted changes
//	0.3.0                      no VCS stamp (go test, go run, no git checkout)
//
// An empty version (a plain `go build`, without the Makefile's ldflags) reads "dev".
func String(version string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Format(version, nil)
	}
	return Format(version, info.Settings)
}

// Format is String with the build settings given (debug.BuildInfo.Settings).
func Format(version string, settings []debug.BuildSetting) string {
	if version == "" {
		version = "dev"
	}
	var revision string
	var modified bool
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return version
	}
	if len(revision) > revisionLen {
		revision = revision[:revisionLen]
	}
	if modified {
		return version + " (" + revision + ", modified)"
	}
	return version + " (" + revision + ")"
}
