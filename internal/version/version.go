// Package version holds build metadata shared by controller, agent and CLI.
package version

// Version is overridden at release build time:
//
//	go build -ldflags "-X continuum/internal/version.Version=v1.2.3"
var Version = "0.0.0-dev"
