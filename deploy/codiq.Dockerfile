# CodiQ's ingestion service (SPEC.md §14 M2), built from the working tree.
#
# Built from source rather than pulled, and not because a registry is missing:
# `codiq` *is* this repository, so the image a compose run should exercise is
# the one built from the checkout in front of you. deploy/gopgql.Dockerfile is
# the opposite case — a dependency, pinned to a ref, temporarily built from
# source only until its release lands.
#
# SPEC.md §11.1's snippet writes this as a bare `build: .`, i.e. a Dockerfile at
# the repository root. It lives under deploy/ beside the gopgql one instead, so
# that everything the local runtime needs is in one directory and the repository
# root stays source.

ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-alpine AS build
# Pure Go, no cgo (SPEC.md §2.7) — the constraint the whole ingestion path is
# chosen for, gotreesitter included. Set explicitly so the image would fail to
# build if something in the path ever stopped honouring it.
ENV CGO_ENABLED=0
WORKDIR /src

# Dependencies first, so editing source does not re-download the module graph.
COPY go.mod go.sum ./
RUN go mod download

# The ingestion path, package by package, rather than `COPY . .`: the docs site
# and its node_modules are most of the repository by size and none of this
# binary, and listing the packages keeps a docs change from invalidating the Go
# build cache. A new package under §12's layout is one line here.
COPY cmd/ ./cmd/
COPY coord/ ./coord/
COPY extract/ ./extract/
COPY facts/ ./facts/
COPY index/ ./index/
COPY link/ ./link/
COPY store/ ./store/

RUN go build -o /out/codiq ./cmd/codiq

FROM alpine:3.21
COPY --from=build /out/codiq /usr/local/bin/codiq
# No ENTRYPOINT and no CMD, matching deploy/gopgql.Dockerfile: every service in
# the compose names its command and its repository argument explicitly.
