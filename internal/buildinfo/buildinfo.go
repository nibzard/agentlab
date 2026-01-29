package buildinfo

import "fmt"

// These values are overridden at build time via -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("version=%s commit=%s date=%s", Version, Commit, Date)
}
