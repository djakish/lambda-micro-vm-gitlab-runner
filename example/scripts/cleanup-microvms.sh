#!/usr/bin/env bash
#
# Best-effort cleanup of the things that live OUTSIDE CloudFormation: MicroVMs
# launched at runtime and the MicroVM image published on first boot. `cdk
# destroy` handles everything else; `pnpm teardown` runs both.
#
# Override the defaults if you changed them in config.ts:
#   AWS_REGION=eu-west-1 MICROVM_IMAGE_NAME=my-image ./scripts/cleanup-microvms.sh
set -uo pipefail

REGION="${AWS_REGION:-us-east-1}"
IMAGE_NAME="${MICROVM_IMAGE_NAME:-gitlab-ci-runner}"

echo "==> Terminating running MicroVMs in ${REGION}"
ids="$(aws lambda-microvms list-microvms --region "$REGION" --output json 2>/dev/null \
  | jq -r '(.microvms // .items // [])[].microvmId' 2>/dev/null || true)"
for id in $ids; do
  echo "    terminate ${id}"
  aws lambda-microvms terminate-microvm --microvm-identifier "$id" --region "$REGION" >/dev/null 2>&1 || true
done

echo "==> Deleting MicroVM image ${IMAGE_NAME}"
aws lambda-microvms delete-microvm-image --image-identifier "$IMAGE_NAME" --region "$REGION" >/dev/null 2>&1 \
  || echo "    (image not deleted automatically — remove it manually if it lingers)"

echo "==> Cleanup complete"
