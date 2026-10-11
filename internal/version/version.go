// Package version identifies the build running in production.
package version

// Version is injected from the GitHub release tag at build time, without its v prefix.
// Local builds deliberately identify themselves as development builds.
var Version = "dev"
