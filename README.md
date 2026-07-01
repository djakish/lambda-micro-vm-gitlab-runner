# GitLab Runner — AWS Lambda MicroVM executor

[![CI](https://github.com/djakish/lambda-micro-vm-gitlab-runner/actions/workflows/ci.yml/badge.svg)](https://github.com/djakish/lambda-micro-vm-gitlab-runner/actions/workflows/ci.yml)

Run each GitLab CI/CD job in its own throwaway [AWS Lambda MicroVM](https://docs.aws.amazon.com/lambda/latest/dg/lambda-microvms-guide.html): a Firecracker-isolated, snapshot-booted environment that starts in a moment, runs the job, and is terminated when the job ends.

This is a [GitLab custom executor](https://docs.gitlab.com/runner/executors/custom/) — the runner host stays tiny and stateless; all job work happens inside per-job MicroVMs with VM-level isolation between tenants.

## Why this exists

Lambda MicroVMs are purpose-built for "multi-tenant CI/CD — task executors that require isolation between tenants." They give you:

- **Strong isolation** — every job gets its own kernel via Firecracker; nothing is shared between jobs.
- **Fast starts** — MicroVMs resume from a pre-initialized snapshot, so the environment (and the exec agent) is already running when the VM appears.
- **No servers to manage** — no warm pool of EC2, no autoscaling group; the runner host only orchestrates.
- **Cost that tracks usage** — you pay compute while a VM runs and terminate it at job end.

## The key design constraint

Lambda MicroVMs expose **no SSH and no native exec API**. The only way in is the VM's dedicated HTTPS endpoint, authenticated with a per-VM [JWE token](https://docs.aws.amazon.com/lambda/latest/dg/microvms-networking.html) (`X-aws-proxy-auth` header, default port 8080).

So this executor ships in **two halves**:

1. **`microvm-agent`** — a tiny HTTP server baked into the MicroVM image. It answers the Lambda lifecycle hooks (`/run`, etc.) and exposes `POST /exec`, which runs a shell script and **streams** stdout/stderr + the exit code back to the caller.
2. **`microvm-executor`** — the driver CLI on the runner host. It implements the four custom-executor stages, calling `aws lambda-microvms` to manage the VM lifecycle and talking to the agent to run job scripts.

```
GitLab Runner host                              AWS Lambda MicroVMs
┌────────────────────────┐                      ┌──────────────────────────────┐
│ gitlab-runner (custom) │                      │   MicroVM (per job)          │
│   │                    │  aws lambda-microvms │  ┌────────────────────────┐  │
│   ├─ config  ──────────┼──────────────────────▶  │ microvm-agent :8080    │  │
│   ├─ prepare ──────────┼─ run-microvm ───────▶│  │  /run, /healthz        │  │
│   │                    │  create-auth-token   │  │  POST /exec  ──▶ bash  │  │
│   ├─ run <script> <s>  ┼─ HTTPS endpoint ─────┼─▶│  stream stdout/stderr  │  │
│   │   (×11 sub-stages) │  X-aws-proxy-auth    │  │  + exit code (JSONL)   │  │
│   └─ cleanup ──────────┼─ terminate-microvm ─▶│  └────────────────────────┘  │
└────────────────────────┘                      └──────────────────────────────┘
```

## How a job flows through the stages

| Stage | Runs | What the driver does |
|-------|------|----------------------|
| `config` | once | Prints `builds_dir` (`/builds`), `cache_dir` (`/cache`), `shell` (`bash`) as JSON. No AWS calls. |
| `prepare` | once | Reaps any VM from a prior attempt; `run-microvm`; saves state; waits for `RUNNING`; mints an auth token scoped to the agent port; waits for the agent `/healthz`. |
| `run` | 11+ | For each sub-stage (`prepare_script`, `get_sources`, `restore_cache`, `build_script`, `after_script`, `archive_cache`, `upload_artifacts`, …) reads the script GitLab generated, POSTs it to the agent `/exec`, streams output live to the job log, and maps the exit code onto GitLab's failure contract. |
| `cleanup` | once (always) | `terminate-microvm` and removes state. Failures here are logged but never fail the job. |

State (MicroVM id, endpoint, token) is handed between these separate processes via a per-job file under `MICROVM_STATE_DIR`, keyed by `CI_PROJECT_ID`/`CI_JOB_ID`. See [`internal/state`](internal/state/state.go).

The exec protocol (JSON Lines over a streamed HTTP body) is documented in [`internal/protocol`](internal/protocol/protocol.go). It was chosen over HTTP trailers because it survives any proxy that preserves ordinary streamed bodies (the MicroVM ingress does — it supports SSE and gRPC) and conveys the exit code inline.

## Repository layout

```
install.sh              one-command bootstrap (build + wizard)
cmd/microvm-executor/   driver CLI (setup/config/prepare/run/cleanup)
cmd/microvm-agent/      in-VM HTTP agent (exec + lifecycle hooks)
internal/setup/         interactive install wizard
internal/agent/         agent handler (exec, streaming, hooks)
internal/agentclient/   driver→agent streaming client
internal/microvm/       thin `aws lambda-microvms` wrapper
internal/config/        env-driven driver configuration
internal/state/         per-job state file handoff
internal/protocol/      the exec wire format shared by both binaries
image/                  MicroVM image Dockerfile + build/publish script
deploy/                 config.toml example + IAM policies
```

## Quickstart

One command builds the driver and walks you through the whole setup — it detects
your AWS account, offers to create the S3 bucket and IAM build role, publishes the
MicroVM image, and writes a ready-to-use `config.toml`:

```bash
git clone https://github.com/djakish/lambda-micro-vm-gitlab-runner.git
cd lambda-micro-vm-gitlab-runner
./install.sh          # or: make setup
```

```
┌─ GitLab Runner · AWS Lambda MicroVM executor ─┐
└─ interactive setup                            ┘

1/5  Preflight
  ✓ found AWS CLI: /usr/local/bin/aws
  ✓ authenticated as arn:aws:iam::123456789012:user/you (account 123456789012)

2/5  MicroVM image
  Build & publish a new MicroVM image now? (Y/n): y
  ✓ created s3://microvm-ci-123456789012-us-east-1
  ✓ created role MicrovmBuildRole with S3 + logs permissions
  ✓ image built: arn:aws:lambda:us-east-1:123456789012:microvm-image:gitlab-ci-runner

3/5  GitLab Runner   …   4/5  Paths & limits   …   5/5  Generate & install
  ✓ wrote ./config.toml
Done 🎉
```

Prefer to do it by hand? The manual steps are below.

## Prerequisites

- An AWS account with Lambda MicroVMs available in your region.
- An S3 bucket for the image build artifact (the wizard can create it).
- Go 1.26+ (to build the binaries) and AWS CLI v2 new enough to include `lambda-microvms`.
- A GitLab Runner host (EC2 recommended, so it can use an instance profile) with the AWS CLI installed and credentials that can drive MicroVMs.

## Manual setup

### 1. Build the MicroVM image

Create the build role (Lambda assumes it to build your image):

```bash
aws iam create-role --role-name MicrovmBuildRole \
  --assume-role-policy-document file://deploy/iam/build-role-trust.json
aws iam put-role-policy --role-name MicrovmBuildRole --policy-name build \
  --policy-document file://deploy/iam/build-role-policy.json   # edit the bucket first
```

Publish the image (Lambda builds the Dockerfile — you don't need Docker locally):

```bash
export AWS_REGION=us-east-1
export MICROVM_S3_BUCKET=my-artifact-bucket
export MICROVM_BUILD_ROLE_ARN=arn:aws:iam::123456789012:role/MicrovmBuildRole
export MICROVM_BASE_IMAGE_ARN=arn:aws:lambda:us-east-1:aws:microvm-image:al2023-1
image/build-and-publish.sh
```

The script prints the resulting **image ARN** — use it as `MICROVM_IMAGE_ARN` below.

The default [image](image/Dockerfile) is Debian + `git`, `git-lfs`, the `gitlab-runner` binary (needed *inside* the VM for `get_sources` and cache/artifact steps), **Docker Engine + buildx** (see [Building Docker images](#building-docker-images)), and the agent. **Add your own build tooling** (compilers, SDKs) in a downstream layer or by editing the Dockerfile.

### 2. Install the driver on the runner host

```bash
make executor
sudo install -D -m 0755 bin/microvm-executor /opt/microvm-executor/bin/microvm-executor
```

Give the runner host permission to drive MicroVMs by attaching
[`deploy/iam/runner-host-policy.json`](deploy/iam/runner-host-policy.json) to its instance profile.
(⚠️ Confirm the IAM action names against the service's IAM reference — see the note in that file.)

### 3. Configure the runner

Copy [`deploy/config.toml.example`](deploy/config.toml.example) to `/etc/gitlab-runner/config.toml`, set your runner token and `MICROVM_IMAGE_ARN`, then restart the runner. That's it.

### 4. Run a job

Any ordinary `.gitlab-ci.yml` works — nothing MicroVM-specific is required:

```yaml
build:
  script:
    - echo "Running inside a Lambda MicroVM"
    - uname -a
    - git --version
```

A job may override the image per-run with a variable:

```yaml
build:
  variables:
    MICROVM_IMAGE: arn:aws:lambda:us-east-1:123456789012:microvm-image:my-custom-image
  script: [ make ]
```

## Cost & lifecycle

- **Running** MicroVMs incur compute charges; **suspended** incur only snapshot storage; **terminated** incur nothing.
- The driver **terminates** the VM at `cleanup`, so the normal cost is just the job's wall-clock.
- `MICROVM_MAX_DURATION_SECONDS` (default 4h, max 8h) is a backstop: Lambda terminates a VM that outlives it, so a hung job or a crashed runner can't leak a VM forever.
- The idle policy auto-suspends a VM with no endpoint traffic; for CI this mainly protects orphans, since active jobs keep the endpoint busy.

## Security

- Each job is a separate Firecracker MicroVM — no shared kernel or filesystem between jobs.
- The endpoint auth token is a JWE **scoped to a single port** with a short TTL (job timeout + buffer), stored `0600` on the runner host.
- Lock job egress to your VPC with an egress network connector (`MICROVM_EGRESS_CONNECTORS`) — see [Networking](https://docs.aws.amazon.com/lambda/latest/dg/microvms-networking.html).
- The runner host holds AWS credentials; the MicroVMs themselves only get the optional `MICROVM_EXECUTION_ROLE_ARN` you grant them.

## Building Docker images

The default image ships **Docker Engine + buildx**, and the entrypoint starts
`dockerd` before the agent — so `docker` just works inside a job, no
`services: docker:dind`, no `--privileged`, no TLS-to-a-sidecar dance. This is
safe here because each job is its own Firecracker MicroVM: the VM *is* the
sandbox, so there is no shared host to escape to. (Docker uses namespaces/cgroups,
not nested virtualization, so it runs fine in a microVM.)

```yaml
build-image:
  variables:
    REGISTRY: 123456789012.dkr.ecr.us-east-1.amazonaws.com
  script:
    # Auth to ECR (VM has internet egress; or grant MICROVM_EXECUTION_ROLE_ARN):
    - aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin "$REGISTRY"
    - docker buildx build -t "$REGISTRY/$CI_PROJECT_PATH:$CI_COMMIT_SHA" --push .
```

Notes:
- **Multi-arch** works via `docker buildx` + QEMU (userspace emulation — no nested virt needed).
- **Sizing:** image builds are heavier than test jobs; run a larger MicroVM size. The guest kernel needs the usual Docker features (overlayfs, br_netfilter) — the standard base image has them.
- **Disable Docker** for lean test-only images by setting `MICROVM_ENABLE_DOCKER=0` (skips starting `dockerd`).
- Kaniko is not used — Google [archived it in June 2025](https://github.com/GoogleContainerTools/kaniko/issues/3348); real Docker in a VM is the simpler, better-isolated path here.

## Limitations & future work

- **`services:` are not started.** `CUSTOM_ENV_CI_JOB_SERVICES` is available to the driver but service containers aren't launched. To support them, launch them inside the VM (Docker is already there) or as companion MicroVMs.
- **Interactive web terminals** are not supported.
- The driver shells out to the **AWS CLI** rather than an SDK, since `lambda-microvms` is new; the CLI must be present on the runner host.

## Development

```bash
make build      # host binaries into ./bin
make test       # unit + end-to-end exec tests (agent ⇄ client over httptest)
make vet
make agent-linux MICROVM_ARCH=arm64   # cross-compile the agent
```

The exec path is covered end-to-end in [`internal/agentclient/exec_test.go`](internal/agentclient/exec_test.go): it runs the real agent handler behind an httptest server and asserts streamed output, exit codes, large-log streaming, and cancellation.

## Versioning & releases

Releases follow [SemVer](https://semver.org/): tag `vMAJOR.MINOR.PATCH`. The
version is stamped into the binary at build time (`-X main.version`) and shown by:

```bash
microvm-executor version
```

CI (`.github/workflows/ci.yml`) runs gofmt/vet/build/test on every push and PR.
Pushing a `v*` tag triggers `.github/workflows/release.yml`, which cross-compiles
`microvm-executor` (linux + darwin) and `microvm-agent` (linux) and attaches the
archives + `checksums.txt` to a GitHub Release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Tagging is optional — `install.sh` builds straight from source — but it gives you
reproducible, checksummed artifacts and lets `go install ...@v0.1.0` pin a version.
