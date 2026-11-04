# End-to-end CLI smoke test, modelling "install tallpine on a fresh machine, then use it".
#
# Build context is the REPO ROOT:
#   docker build -f test/cli/Dockerfile -t tallpine-cli-smoke .
#   docker run --rm --init tallpine-cli-smoke
#
# `tallpine start` is now the desktop-app launcher (it no longer runs a daemon); the
# fresh-box check just proves the binary is sane and start reaches the fetch
# path. --init keeps process accounting clean for any short-lived child.

# ---- stage 1: build the binary (the "release" a user would download) ----
ARG GO_IMAGE=golang:1.27.3-bookworm
ARG RUNTIME_IMAGE=debian:bookworm-slim
FROM ${GO_IMAGE} AS build
WORKDIR /src

# Cache modules first.
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download

COPY backend ./backend
# Pure-Go SQLite (modernc) builds fine with CGO disabled -> a static binary.
# Build against a release repo with NO published assets so `tallpine start` reaches the
# fetch path and fails cleanly (404) on a fresh box. The real AgentWrapper repo
# now publishes a linux-x64 AppImage, so an unpinned binary would download it and
# exit 0, defeating the fresh-install assertion below. See install-check.sh.
RUN cd backend && CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X tallpine.eu/tallpine/backend/internal/cli.releaseRepo=AgentWrapper/tallpine-fresh-install-fixture" \
      -o /out/tallpine ./cmd/tallpine

# ---- stage 2: a clean machine with NO Go toolchain, just like an end user ----
FROM ${RUNTIME_IMAGE} AS run

# Runtime deps a fresh user would need: git is required by `tallpine doctor`; curl
# drives the HTTP-level guard checks; ca-certificates for good measure. Zellij is
# optional for this smoke test, so doctor reports a WARN if it is absent.
RUN apt-get update \
 && apt-get install -y --no-install-recommends git curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# "Install" the CLI the way a user would: drop the binary on PATH.
COPY --from=build /out/tallpine /usr/local/bin/tallpine
COPY test/cli/install-check.sh /usr/local/bin/tallpine-install-check.sh
RUN chmod +x /usr/local/bin/tallpine /usr/local/bin/tallpine-install-check.sh

# Run as an unprivileged user with a real HOME, like a normal install.
RUN useradd --create-home --shell /bin/bash tallpine
USER tallpine
WORKDIR /home/vo

# Sanity: prove the install resolved before the check runs.
RUN tallpine version

ENTRYPOINT ["/usr/local/bin/tallpine-install-check.sh"]
