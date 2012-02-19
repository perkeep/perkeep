package uistatic

import (
	"camlistore.org/pkg/misc/fileembed"

	"appengine"
)

var Files = &fileembed.Files{
	DirFallback: "uistatic",

	// In dev_appserver, allow edit-and-reload without
	// restarting. In production, though, it's faster to just
	// slurp it in.
	SlurpToMemory: !appengine.IsDevAppServer(),
}
