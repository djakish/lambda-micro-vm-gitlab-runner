#!/usr/bin/env bash
#
# Build the MicroVM image and register it with Lambda MicroVMs.
#
# Steps: assemble a build context (Dockerfile + Go source) -> zip -> upload to
# S3 -> create-microvm-image -> poll until CREATED. The Dockerfile is built by
# Lambda, not locally, so you do not need Docker on the machine running this.
#
# Required environment / flags:
#   MICROVM_IMAGE_NAME     name to register (default: gitlab-ci-runner)
#   MICROVM_S3_BUCKET      S3 bucket for the code artifact (required)
#   MICROVM_BUILD_ROLE_ARN IAM role Lambda assumes to build (required)
#   MICROVM_BASE_IMAGE_ARN Lambda-managed base image ARN (required)
#   AWS_REGION             target region (required)
#
# Usage: image/build-and-publish.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

IMAGE_NAME="${MICROVM_IMAGE_NAME:-gitlab-ci-runner}"
: "${MICROVM_S3_BUCKET:?set MICROVM_S3_BUCKET to the artifact bucket}"
: "${MICROVM_BUILD_ROLE_ARN:?set MICROVM_BUILD_ROLE_ARN to the Lambda build role ARN}"
: "${MICROVM_BASE_IMAGE_ARN:?set MICROVM_BASE_IMAGE_ARN to the Lambda base image ARN}"
: "${AWS_REGION:?set AWS_REGION}"

S3_KEY="${MICROVM_S3_KEY:-microvm-images/${IMAGE_NAME}.zip}"
S3_URI="s3://${MICROVM_S3_BUCKET}/${S3_KEY}"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

echo "==> Assembling build context"
# The Dockerfile expects the module root as its context. Copy only what the
# multi-stage build needs to keep the artifact small.
cp "${REPO_ROOT}/image/Dockerfile" "${workdir}/Dockerfile"
cp "${REPO_ROOT}/go.mod" "${workdir}/go.mod"
[ -f "${REPO_ROOT}/go.sum" ] && cp "${REPO_ROOT}/go.sum" "${workdir}/go.sum"
cp -R "${REPO_ROOT}/internal" "${workdir}/internal"
cp -R "${REPO_ROOT}/cmd" "${workdir}/cmd"

archive="${workdir}/artifact.zip"
echo "==> Creating artifact ${archive}"
( cd "$workdir" && zip -q -r "$archive" Dockerfile go.mod go.sum internal cmd 2>/dev/null || \
  ( cd "$workdir" && zip -q -r "$archive" Dockerfile go.mod internal cmd ) )

echo "==> Uploading to ${S3_URI}"
aws s3 cp "$archive" "$S3_URI" --region "$AWS_REGION"

echo "==> Creating MicroVM image '${IMAGE_NAME}'"
aws lambda-microvms create-microvm-image \
  --name "$IMAGE_NAME" \
  --code-artifact "uri=${S3_URI}" \
  --base-image-arn "$MICROVM_BASE_IMAGE_ARN" \
  --build-role-arn "$MICROVM_BUILD_ROLE_ARN" \
  --region "$AWS_REGION" \
  --output json

echo "==> Waiting for image build to reach CREATED (logs: /aws/lambda/microvms/${IMAGE_NAME})"
while true; do
  state="$(aws lambda-microvms get-microvm-image \
    --image-identifier "$IMAGE_NAME" \
    --region "$AWS_REGION" \
    --output json | jq -r '.state')"
  echo "    state=${state}"
  case "$state" in
    CREATED)
      break ;;
    CREATE_FAILED)
      echo "!! image build failed; check CloudWatch logs" >&2
      exit 1 ;;
  esac
  sleep 10
done

echo "==> Done. Image ARN:"
aws lambda-microvms get-microvm-image \
  --image-identifier "$IMAGE_NAME" \
  --region "$AWS_REGION" \
  --output json | jq -r '.imageArn'
