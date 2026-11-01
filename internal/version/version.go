// Package version reports build identity. Values are injected at link time.
package version

import "fmt"

// Build metadata. Overridden with -ldflags "-X ...".
var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// String renders the one-line version banner.
func String() string {
	return fmt.Sprintf("cosmics %s (commit %s, built %s)", Version, Commit, BuildDate)
}
