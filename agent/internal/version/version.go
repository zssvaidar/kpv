// Package version reports the build version of the agent.
package version

// Current is the version the agent reports. It is a placeholder until the agent
// is packaged for distribution (issue #55).
const Current = "0.0.0-dev"

// String returns the version the agent reports.
func String() string {
	return Current
}
