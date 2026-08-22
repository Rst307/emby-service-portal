// Package buildinfo exposes build-time identity for the running binary.
//
// Version and Commit are injected at build time via -ldflags:
//
//	go build -ldflags "-X github.com/Rst307/emby-service-portal/internal/buildinfo.Version=v1.2.3"
//
// A binary built without injection reports Version "dev".
package buildinfo

// Version is the release tag this binary was built from ("dev" for local
// builds). Set with -X at build time; also serves as the baseline for the
// self-update version comparison.
var Version = "dev"

// Commit is the short git commit hash the binary was built from (may be empty).
var Commit = ""
