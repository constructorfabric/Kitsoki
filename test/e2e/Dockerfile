# test/e2e/Dockerfile — reusable end-to-end packaging test for the kitsoki CLI.
#
# Two stages, each proving one half of "the system dependencies are correct":
#
#   builder  — a faithful build environment with the *build* dependencies the
#              Makefile documents: Go 1.25 + Node + pnpm. It runs `make install`,
#              the real shipping build path (which builds & embeds the runstatus
#              SPA via pnpm, then compiles the binary). If a build dep is wrong
#              or missing, the image fails to build here.
#
#   runtime  — a slim glibc base (kitsoki uses the pure-Go modernc SQLite driver,
#              so no cgo/libsqlite needed) carrying only the *runtime*
#              dependencies kitsoki shells out to: git, bash (required) and gh
#              (optional). The binary + the deterministic flow fixtures are
#              copied in, and smoke.sh is the entrypoint.
#
# Build context is the repo root:
#   DOCKER_BUILDKIT=1 docker build -f test/e2e/Dockerfile -t kitsoki-e2e .
# See test/e2e/run.sh for the one-shot build-and-run wrapper.

# ---------------------------------------------------------------------------
FROM golang:1.25-bookworm AS builder

# Build deps: Node (for the SPA) + pnpm (via corepack) + git (module fetches).
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git gnupg \
 && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && corepack enable && corepack prepare pnpm@11.3.0 --activate \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Warm the module cache first (better layer caching on source-only changes).
COPY go.mod go.sum ./
RUN go mod download

# Then the full source and build via the documented path.
COPY . .
RUN make install INSTALLDIR=/out \
 && /out/kitsoki version

# ---------------------------------------------------------------------------
FROM debian:bookworm-slim AS runtime

# Runtime deps kitsoki exec()s: git + bash (required), gh (optional).
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git bash curl gnupg \
 && mkdir -p -m 755 /etc/apt/keyrings \
 && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update && apt-get install -y --no-install-recommends gh \
 && apt-get purge -y gnupg && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/kitsoki /usr/local/bin/kitsoki

# The deterministic flow fixtures are read from disk at runtime.
WORKDIR /opt/kitsoki
COPY --from=builder /src/testdata/apps ./testdata/apps
COPY test/e2e/smoke.sh /usr/local/bin/kitsoki-e2e-smoke
RUN chmod +x /usr/local/bin/kitsoki-e2e-smoke

ENV KITSOKI_E2E_ROOT=/opt/kitsoki
ENTRYPOINT ["/usr/local/bin/kitsoki-e2e-smoke"]
