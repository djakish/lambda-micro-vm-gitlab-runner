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
# environment: git + git-lfs (get_sources), the gitlab-runner binary
# (cache/artifact steps), and Docker Engine + buildx for image builds. Because
# each job runs in its own Firecracker MicroVM, running dockerd here is safe and
# needs no privileged/dind-service tricks — the VM *is* the isolation boundary.
# (Docker uses namespaces/cgroups, not nested virtualization, so it runs fine in
# a microVM.) Add your own build tooling in a downstream layer.

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
ARG DEBIAN_RELEASE=bookworm
# Pin a gitlab-runner version, or use "latest". Must be in PATH for cache/artifacts.
ARG GITLAB_RUNNER_CHANNEL=latest

# Base CI tools + gitlab-runner + Docker Engine (dockerd) + buildx plugin.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        git-lfs \
        tar \
        gzip \
        openssh-client \
        iptables \
        uidmap \
    && git lfs install --system --skip-repo \
    && curl -fsSL "https://gitlab-runner-downloads.s3.amazonaws.com/${GITLAB_RUNNER_CHANNEL}/binaries/gitlab-runner-linux-${ARCH}" \
        -o /usr/local/bin/gitlab-runner \
    && chmod +x /usr/local/bin/gitlab-runner \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian ${DEBIAN_RELEASE} stable" \
        > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        docker-ce \
        docker-ce-cli \
        containerd.io \
        docker-buildx-plugin \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/microvm-agent /usr/local/bin/microvm-agent

# Entrypoint: bring up dockerd (unless disabled), wait until it answers, then
# hand off to the agent. dockerd is started before the agent so it is part of
# the Lambda snapshot and ready the instant a job's first `docker` command runs.
COPY <<'EOF' /usr/local/bin/entrypoint.sh
#!/usr/bin/env bash
set -uo pipefail

if [ "${MICROVM_ENABLE_DOCKER:-1}" = "1" ]; then
  echo "entrypoint: starting dockerd"
  dockerd >/var/log/dockerd.log 2>&1 &
  for _ in $(seq 1 60); do
    docker info >/dev/null 2>&1 && { echo "entrypoint: dockerd is ready"; break; }
    sleep 0.5
  done
  docker info >/dev/null 2>&1 || echo "entrypoint: WARNING dockerd not ready; see /var/log/dockerd.log"
fi

exec /usr/local/bin/microvm-agent
EOF
RUN chmod +x /usr/local/bin/entrypoint.sh

# builds_dir and cache_dir from config.toml live here, inside the VM.
RUN mkdir -p /builds /cache

ENV MICROVM_AGENT_ADDR=:8080 \
    MICROVM_AGENT_WORKDIR=/builds \
    MICROVM_ENABLE_DOCKER=1

# Lambda routes endpoint traffic to 8080 by default; the agent listens there.
EXPOSE 8080

CMD ["/usr/local/bin/entrypoint.sh"]
