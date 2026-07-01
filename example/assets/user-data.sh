#!/bin/bash
#
# Runner-host bootstrap (Amazon Linux 2023). Placeholders (__NAME__) are filled
# in by lib/user-data.ts at synth time. Output is in /var/log/cloud-init-output.log.
set -euxo pipefail

REGION="__REGION__"
ARTIFACT_BUCKET="__BUCKET__"
BUILD_ROLE_ARN="__BUILD_ROLE_ARN__"
BASE_IMAGE_ARN="__BASE_IMAGE_ARN__"
IMAGE_NAME="__IMAGE_NAME__"
IMAGE_ARN="__IMAGE_ARN__"
GITLAB_URL="__GITLAB_URL__"
TOKEN_SSM_PARAM="__TOKEN_SSM_PARAM__"
CONCURRENT="__CONCURRENT__"
MAX_DURATION="__MAX_DURATION__"
STATE_DIR="__STATE_DIR__"
REPO_URL="__REPO_URL__"
GO_VERSION="__GO_VERSION__"
GO_ARCH="__GO_ARCH__"
RUNNER_ARCH="__RUNNER_ARCH__"
AWSCLI_ARCH="__AWSCLI_ARCH__"
EXECUTOR_BIN="/opt/microvm-executor/bin/microvm-executor"

echo "=== [1/7] system packages ==="
dnf -y update
dnf -y install git tar gzip unzip jq zip

echo "=== [2/7] AWS CLI v2 ==="
if ! command -v aws >/dev/null 2>&1; then
  curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${AWSCLI_ARCH}.zip" -o /tmp/awscliv2.zip
  unzip -q /tmp/awscliv2.zip -d /tmp
  /tmp/aws/install --update
fi

echo "=== [3/7] Go toolchain ==="
curl -fsSL "https://go.dev/dl/go${GO_VERSION}.${GO_ARCH}.tar.gz" -o /tmp/go.tgz
rm -rf /usr/local/go
tar -C /usr/local -xzf /tmp/go.tgz
export PATH="${PATH}:/usr/local/go/bin"

echo "=== [4/7] build microvm-executor from source ==="
rm -rf /opt/src/executor
git clone --depth 1 "$REPO_URL" /opt/src/executor
mkdir -p /opt/microvm-executor/bin
( cd /opt/src/executor && /usr/local/go/bin/go build -trimpath -ldflags "-s -w" -o "$EXECUTOR_BIN" ./cmd/microvm-executor )

echo "=== [5/7] publish MicroVM image (only if absent) ==="
if ! aws lambda-microvms get-microvm-image --image-identifier "$IMAGE_NAME" --region "$REGION" >/dev/null 2>&1; then
  AWS_REGION="$REGION" \
  MICROVM_S3_BUCKET="$ARTIFACT_BUCKET" \
  MICROVM_BUILD_ROLE_ARN="$BUILD_ROLE_ARN" \
  MICROVM_BASE_IMAGE_ARN="$BASE_IMAGE_ARN" \
  MICROVM_IMAGE_NAME="$IMAGE_NAME" \
    /opt/src/executor/image/build-and-publish.sh
fi

echo "=== [6/7] install gitlab-runner and write config ==="
curl -fsSL "https://gitlab-runner-downloads.s3.amazonaws.com/latest/binaries/gitlab-runner-linux-${RUNNER_ARCH}" \
  -o /usr/local/bin/gitlab-runner
chmod +x /usr/local/bin/gitlab-runner
mkdir -p /etc/gitlab-runner "$STATE_DIR"

RUNNER_TOKEN="$(aws ssm get-parameter --name "$TOKEN_SSM_PARAM" --with-decryption \
  --query 'Parameter.Value' --output text --region "$REGION")"

cat > /etc/gitlab-runner/config.toml <<CONFIG
concurrent = ${CONCURRENT}
check_interval = 3

[[runners]]
  name = "aws-lambda-microvm"
  url = "${GITLAB_URL}"
  token = "${RUNNER_TOKEN}"
  executor = "custom"
  builds_dir = "/builds"
  cache_dir = "/cache"
  environment = [
    "MICROVM_REGION=${REGION}",
    "MICROVM_IMAGE_ARN=${IMAGE_ARN}",
    "MICROVM_MAX_DURATION_SECONDS=${MAX_DURATION}",
    "MICROVM_STATE_DIR=${STATE_DIR}",
  ]

  [runners.custom]
    config_exec = "${EXECUTOR_BIN}"
    config_args = ["config"]
    config_exec_timeout = 60
    prepare_exec = "${EXECUTOR_BIN}"
    prepare_args = ["prepare"]
    prepare_exec_timeout = 300
    run_exec = "${EXECUTOR_BIN}"
    run_args = ["run"]
    cleanup_exec = "${EXECUTOR_BIN}"
    cleanup_args = ["cleanup"]
    cleanup_exec_timeout = 120
    graceful_kill_timeout = 300
    force_kill_timeout = 60
CONFIG

echo "=== [7/7] start the runner service ==="
gitlab-runner install --user root --working-directory /home 2>/dev/null || true
gitlab-runner start || systemctl restart gitlab-runner || true
echo "=== runner host ready ==="
