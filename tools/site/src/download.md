---
layout: doc
---

# Download Kitsoki

Prebuilt `kitsoki` binaries are published on GitHub Releases for the normal local-use platforms.

| Platform | Architecture | Download |
|---|---:|---|
| macOS | Apple Silicon | [Download `kitsoki_darwin_arm64.tar.gz`](https://github.com/bsacrobatix/Kitsoki/releases/latest/download/kitsoki_darwin_arm64.tar.gz) |
| macOS | Intel | [Download `kitsoki_darwin_amd64.tar.gz`](https://github.com/bsacrobatix/Kitsoki/releases/latest/download/kitsoki_darwin_amd64.tar.gz) |
| Linux | x86_64 | [Download `kitsoki_linux_amd64.tar.gz`](https://github.com/bsacrobatix/Kitsoki/releases/latest/download/kitsoki_linux_amd64.tar.gz) |
| Linux | ARM64 | [Download `kitsoki_linux_arm64.tar.gz`](https://github.com/bsacrobatix/Kitsoki/releases/latest/download/kitsoki_linux_arm64.tar.gz) |
| Windows | x86_64 | [Download `kitsoki_windows_amd64.zip`](https://github.com/bsacrobatix/Kitsoki/releases/latest/download/kitsoki_windows_amd64.zip) |

[Checksums](https://github.com/bsacrobatix/Kitsoki/releases/latest/download/checksums.txt) · [Open the latest release](https://github.com/bsacrobatix/Kitsoki/releases/latest)

## Install

Download the archive for your platform, extract it, then put `kitsoki` on your `PATH`.

```sh
kitsoki version
```

Verify the archive before installing when possible:

```sh
sha256sum -c checksums.txt
```

## Build From Source

If you want to build from the repository instead:

```sh
make setup
make install
```

See [Getting Started](./guide/getting-started.html) for the full local setup path.
