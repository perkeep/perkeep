package mgo

func HackSocketsPerServer(newLimit int) (restore func()) {
	oldLimit := newLimit
	restore = func() {
		socketsPerServer = oldLimit
	}
	socketsPerServer = newLimit
	return
}
