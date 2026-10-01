# Local patches to cgofuse v1.6.0 (MIT, github.com/winfsp/cgofuse)

1. `fuse/host_cgo.go`: under `CGOFUSE_FUSET_FIRST`, dlopen `/usr/local/lib/libfuse-t.dylib` before
   `/usr/local/lib/libfuse.2.dylib`, so a Mac with both macFUSE (kext not approved) and FUSE-T installed uses FUSE-T.
2. `fuse/fuset.go`: defines `CGOFUSE_FUSET_FIRST` for `darwin && cgo && fuset` builds.
3. Test files removed (upstream tests mount real filesystems).

Used by `go build -tags fuset` on macOS only (go.mod `replace`). Default builds (`CGO_ENABLED=0`) do not compile cgofuse.
