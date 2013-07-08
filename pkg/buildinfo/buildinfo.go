// Package buildinfo provides information about the current build.
package buildinfo

// GitInfo is either the empty string (the default)
// or is set to the git hash of the most recent commit
// using the -X linker flag. For example, it's set like:
// $ go install --ldflags="-X camlistore.org/pkg/buildinfo.GitInfo "`./misc/gitversion` camlistore.org/server/camlistored
var GitInfo string

// Version returns the git version of this binary.
// If the linker flags were not provided, the return value is "unknown".
func Version() string {
	if GitInfo != "" {
		return GitInfo
	}
	return "unknown"
}
