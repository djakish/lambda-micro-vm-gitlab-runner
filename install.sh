#!/usr/bin/env bash
#
# One-command installer for the GitLab Runner · AWS Lambda MicroVM executor.
# Builds the driver binary, then launches the interactive setup wizard.
#
# Usage:  ./install.sh            (interactive)
#         ./install.sh --yes      (accept all defaults)
set -euo pipefail

cd "$(dirname "$0")"

if ! command -v go >/dev/null 2>&1; then
  echo "Go 1.26+ is required to build the executor: https://go.dev/dl/" >&2
  exit 1
fi
if ! command -v aws >/dev/null 2>&1; then
  echo "AWS CLI v2 (with lambda-microvms) is required: https://aws.amazon.com/cli/" >&2
  exit 1
fi

echo "==> Building microvm-executor…"
go build -trimpath -ldflags "-s -w" -o bin/microvm-executor ./cmd/microvm-executor

echo "==> Launching setup wizard…"
exec ./bin/microvm-executor setup "$@"
