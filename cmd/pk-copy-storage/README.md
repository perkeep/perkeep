# pk-copy-storage

`pk-copy-storage` is a tiny wrapper for `perkeepd admin copy-storage`.
The storage-copying implementation lives in `server/perkeepd`; this directory
is not a Go program.

`go run make.go` installs the wrapper beside `perkeepd`, and release archives
include it. Keep the two files in the same directory.

If installing with `go install ./server/perkeepd` instead, copy
`pk-copy-storage` from this directory beside the installed server and make it
executable (`chmod +x pk-copy-storage`). On Windows, copy
`pk-copy-storage.cmd` beside `perkeepd.exe` instead.

Alternatively, use `perkeepd admin copy-storage` directly. Run
`perkeepd admin copy-storage --help` for usage.
