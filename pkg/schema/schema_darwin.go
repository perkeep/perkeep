//+build darwin
//+build !appengine

package schema

import (
	"os"
	"syscall"
	"time"
)

func init() {
	populateSchemaStat = append(populateSchemaStat, populateSchemaCtime)
}

func populateSchemaCtime(m map[string]interface{}, fi os.FileInfo) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}

	// Include the ctime too, if it differs.
	sec, nsec := st.Ctimespec.Unix()
	ctime := time.Unix(sec, nsec)
	if sec != 0 && !ctime.Equal(fi.ModTime()) {
		m["unixCtime"] = RFC3339FromTime(ctime)
	}
}
