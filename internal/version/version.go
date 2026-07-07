// Package version pins the router's own release version.
//
// The whole fleet shares one scheme, <channel>.<major>.<minor>:
// the leading number is the release channel (1 = alpha, 2 = beta), the
// second is the major release, and the third is the minor patch/hotfix.
// Games advertise their versions via games.toml's `version` key (validated
// by internal/registry); this constant covers the router itself.
package version

// Version is the router's current release. The router is in alpha.
const Version = "1.0.0"

// Channel is the human-readable release channel derived from Version's
// leading component.
const Channel = "alpha"
