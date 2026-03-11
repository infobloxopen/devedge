package version

import "fmt"

var (
	// Set via -ldflags at build time.
	Version = "dev"
	Commit  = "unknown"
)

func String() string {
	return fmt.Sprintf("devedge %s (%s)", Version, Commit)
}
