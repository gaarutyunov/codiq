# gopgql, built from source.
#
# SPEC.md §11.1 and §14 M1 both say the compose pulls gopgql from ghcr. gopgql's
# release pipeline is merged but no `v*` tag has been pushed yet, so
# `ghcr.io/gaarutyunov/gopgql` does not exist. §14 M1 anticipates exactly this:
# "If gopgql has no stable release with a Docker image pushed to the registry
# yet, install it from source ... and proceed — do not block M1 on it."
#
# ==========================================================================
#  TO SWAP BACK TO THE PUBLISHED IMAGE once gopgql cuts a release:
#  delete this file and the `build:` blocks in docker-compose.yml, and give
#  the `migrate` and `mcp` services
#
#      image: ghcr.io/gaarutyunov/gopgql:<pinned>
#
#  instead. That image carries the same two binaries in /usr/local/bin/ and
#  has no ENTRYPOINT and no CMD, so the service `command:` lines below keep
#  working verbatim. Nothing else in deploy/ changes.
# ==========================================================================
#
# The ref is pinned to a commit rather than a branch: a floating `@main` would
# make `docker compose build --no-cache` a different stack on different days,
# which is the thing pinning postgres:19beta2 exists to avoid.

ARG GO_VERSION=1.25
ARG GOPGQL_REF=85a2f68c18f1e0c987ff41c22818f132791674aa

FROM golang:${GO_VERSION}-alpine AS build
ARG GOPGQL_REF
ENV CGO_ENABLED=0
# Both binaries, because M1 uses the pair: `gopgql` applies the migrations and
# `gopgql-mcp` serves the graph.
RUN go install github.com/gaarutyunov/gopgql/cmd/gopgql@${GOPGQL_REF} \
 && go install github.com/gaarutyunov/gopgql/cmd/gopgql-mcp@${GOPGQL_REF}

FROM alpine:3.21
COPY --from=build /go/bin/gopgql /go/bin/gopgql-mcp /usr/local/bin/
# No ENTRYPOINT and no CMD, matching the published image: every service in the
# compose names its command explicitly, and a bare `docker run` fails loudly
# rather than starting a shell.
