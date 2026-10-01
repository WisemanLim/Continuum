# Optional components (FR-R07)

Continuum binaries are static and self-contained. The components below are **not bundled**; install them only
for the feature that needs them. Licenses are listed because some of them restrict redistribution.

| Component | Needed for | Install | License |
|---|---|---|---|
| fuse3 (Linux) | native VFS mount `continuum vfs mount` | `apt install fuse3` / `dnf install fuse3` | GPL-2.0 (kernel) / LGPL-2.1 (libfuse) |
| macFUSE ≥ 4 (macOS) | native VFS mount on macOS | <https://macfuse.github.io> — approve the system extension (System Settings → Privacy & Security), or on macFUSE ≥ 5.4 / macOS 26 enable it under General → Login Items & Extensions → File System Extensions (FSKit, no kext) | macFUSE license (free use; redistribution only with permission) |
| FUSE-T (macOS, experimental) | `-tags fuset` cgo build only | <https://www.fuse-t.org> — **disabled by default**: its NFS loopback panicked the macOS 26.6 kernel (`nfs_vinvalbuf2`) in testing; opt-in `CONTINUUM_VFS_FUSET=i-accept-kernel-panic-risk` on disposable machines only | FUSE-T license (free personal use; commercial bundling is paid) |
| WinFsp (Windows) | native VFS (planned) | <https://winfsp.dev> | GPL-3.0 with FLOSS exception (commercial license for non-FLOSS bundling) |
| Samba + mergerfs | SMB access point (Finder / Explorer) | built as the `continuum-samba:local` container by `make local-build` (Docker) | GPL-3.0 / ISC — separate process, not linked |
| mergerfs (Linux host) | `continuum access local-mount` | `apt install mergerfs` | ISC |
| smartmontools | SMART health and temperature | `brew install smartmontools` / `apt install smartmontools` / Windows installer | GPL-2.0 — executed, not linked |
| Docker | SMB access point container | Docker Desktop / Engine | — |

Continuum detects each component at runtime and explains how to install it when it is missing (for example
`vfs.Available()` and the access-point preflight). Nothing is downloaded automatically.
