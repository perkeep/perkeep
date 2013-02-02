package buildinfo

var GitInfo string

func Version() string {
	if GitInfo != "" {
		return GitInfo
	}
	return "unknown" // TODO: show binary's date?
}
