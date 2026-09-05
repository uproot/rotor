// Package version is the single source of truth for rotor's release
// version. It is defined in code (not injected via ldflags) so every build
// path — `go build`, `go install`, GoReleaser — reports the same version.
package version

// Version is rotor's release version, without the leading "v".
//
// Bumping this constant is part of cutting a release: the release workflow
// refuses to publish a tag that doesn't match it (tag `vX.Y.Z` must equal
// "v" + Version).
const Version = "2.3.0"
