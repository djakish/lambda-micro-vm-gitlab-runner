# syntax=docker/dockerfile:1
#
# MicroVM image for the GitLab custom executor.
#
# Lambda runs this Dockerfile during `create-microvm-image`, starts the CMD, and
# snapshots the running state. The snapshot must therefore have the exec agent
# already listening — that is what makes MicroVM startup near-instant.
#
# The build context is the repository module root (go.mod at ./). The publish
# script (build-and-publish.sh) assembles that context and uploads it to S3.
#
# Tools installed here are the ones GitLab needs *inside* the execution
# environment: git + git-lfs (get_sources) and the gitlab-runner binary
# (cache/artifact steps). Add your own build tooling in a downstream layer.

ARG GO_VERSION=1.26
ARG DEBIAN_RELEASE=bookworm

# --- build the agent from source (no local cross-compile needed) -------------
FROM golang:${GO_VERSION}-${DEBIAN_RELEASE} AS build
WORKDIR /src
COPY go.mod ./
COPY internal ./internal
COPY cmd ./cmd
# Static, stripped binary so it runs on the slim runtime layer and snapshots small.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/microvm-agent ./cmd/microvm-agent

# --- runtime rootfs ----------------------------------------------------------
FROM debian:${DEBIAN_RELEASE}-slim

# ARCH must match the MicroVM base image architecture (amd64 or arm64).
ARG ARCH=amd64
# Pin a gitlab-runner version, or use "latest". Must be in PATH for cache/artifacts.
ARG GITLAB_RUNNER_CHANNEL=latest

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        git-lfs \
        tar \
        gzip \
        openssh-client \
    && git lfs install --system --skip-repo \
    && curl -fsSL "https://gitlab-runner-downloads.s3.amazonaws.com/${GITLAB_RUNNER_CHANNEL}/binaries/gitlab-runner-linux-${ARCH}" \
        -o /usr/local/bin/gitlab-runner \
    && chmod +x /usr/local/bin/gitlab-runner \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/microvm-agent /usr/local/bin/microvm-agent

# builds_dir and cache_dir from config.toml live here, inside the VM.
RUN mkdir -p /builds /cache

ENV MICROVM_AGENT_ADDR=:8080 \
    MICROVM_AGENT_WORKDIR=/builds

# Lambda routes endpoint traffic to 8080 by default; the agent listens there.
EXPOSE 8080

CMD ["/usr/local/bin/microvm-agent"]
