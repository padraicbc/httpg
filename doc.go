// Package httpg provides fluent HTTP requests with inspection, cURL export,
// retries and replay. Sessions reuse connections and cookies. Request and
// response bodies can be inspected without consuming them.
package httpg

import "runtime/debug"

// Version is the httpg module version, taken from the git tag the module was
// built from (for example "v0.1.0"). It is "devel" when built from a working
// tree of this repository. It is sent in the default User-Agent.
var Version = moduleVersion()

func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	const path = "github.com/padraicbc/httpg"
	if info.Main.Path == path {
		return "devel"
	}
	for _, dep := range info.Deps {
		if dep.Path == path {
			if dep.Replace != nil && dep.Replace.Version != "" && dep.Replace.Version != "(devel)" {
				return dep.Replace.Version
			}
			if dep.Version != "" && dep.Version != "(devel)" {
				return dep.Version
			}
		}
	}
	return "devel"
}
