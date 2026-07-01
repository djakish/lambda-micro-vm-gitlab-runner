# Quickstart — MicroVM GitLab runner on AWS (CDK)

Two small, independent CDK stacks:

- **`<project>-runner`** — a tiny always-on EC2 host that auto-installs the AWS CLI, the executor, and `gitlab-runner`, builds the MicroVM image, and runs jobs. Deploy **once**, in the account where your runners live.
- **`<project>-oidc`** — a GitLab OIDC provider + deploy role, so pipelines deploy to AWS with **no stored access keys**. Optional, and usually deployed into the account(s) you deploy *to* (see [multi-account](#runners-in-one-account-deploys-in-many)).

You edit **one file** (`lib/config.ts`) and run a couple of commands. Everything is tagged (via CDK [`Tags`](https://docs.aws.amazon.com/cdk/v2/guide/tagging.html)) and removed by `cdk destroy`.

> Custom scripts are run with `pnpm run <name>` (plain `pnpm deploy` is a different, built-in pnpm command).

## Prerequisites

- AWS credentials configured locally, and the account [CDK-bootstrapped](https://docs.aws.amazon.com/cdk/v2/guide/bootstrapping.html).
- Node 20+ and `pnpm`.
- A GitLab **runner authentication token** (`glrt-…`): GitLab → *Settings → CI/CD → Runners → New runner*.

## 1. Store the runner token (kept out of code)

```bash
aws ssm put-parameter \
  --name /microvm-ci/gitlab-token \
  --type SecureString \
  --value "glrt-XXXXXXXXXXXX" \
  --region us-east-1
```

## 2. Edit `lib/config.ts`

The only file you touch. On gitlab.com you mostly just set `deployRole.oidcSubject` (your `group/project`) and maybe `region`.

- **Just the EC2 runner, no OIDC?** Set `deployRole.enabled: false`.
- **Runners and deploys in the same account?** Leave `enabled: true` — both stacks deploy together.
- **Deploy to other accounts?** See [below](#runners-in-one-account-deploys-in-many).

## 3. Deploy

```bash
pnpm install
pnpm exec cdk bootstrap   # first time in this account/region only
pnpm run deploy           # deploys all stacks in this account
```

Or deploy one stack: `pnpm exec cdk deploy microvm-ci-runner`.

Give the host a few minutes on first boot (it builds the executor and publishes the MicroVM image). Watch it with the printed `ConnectCommand` (SSM Session Manager) and `tail -f /var/log/cloud-init-output.log`.

## 4. Use it in a pipeline

Copy [`assets/gitlab-ci-example.yml`](assets/gitlab-ci-example.yml) into a project's `.gitlab-ci.yml`, set `AWS_ROLE_ARN` to the **`DeployRoleArn`** output, and push. Each job runs in its own MicroVM; the `deploy` job assumes AWS via OIDC:

```yaml
deploy:
  id_tokens:
    AWS_ID_TOKEN: { aud: https://gitlab.com }
  variables:
    AWS_ROLE_ARN: <DeployRoleArn output>
  script:
    - echo "$AWS_ID_TOKEN" > /tmp/oidc_token
    - export AWS_WEB_IDENTITY_TOKEN_FILE=/tmp/oidc_token
    - aws sts get-caller-identity   # runs as the deploy role — no keys
```

## Runners in one account, deploys in many

This is the important bit. **OIDC federation is between GitLab and the account you deploy *to*** — not the account the runners live in. The CI job assumes the deploy role *from inside the MicroVM* (job → target-account STS, using the GitLab token). The runner account is never in that path; it only launches the VM.

So the two stacks are deployed separately:

**1. Runner account** — deploy the runner once. If OIDC lives elsewhere, turn it off here:

```ts
deployRole: { enabled: false, /* ... */ }
```
```bash
pnpm exec cdk deploy microvm-ci-runner --profile runner-account
```

**2. Each target account** — deploy the OIDC stack there, pointed at that account and scoped to the right project:

```ts
deployRole: {
  enabled: true,
  account: '222222222222',                 // the target account
  oidcSubject: 'project_path:client-a/app:ref_type:branch:ref:main',
  // If gitlab.com is already an OIDC provider in that account:
  // existingProviderArn: 'arn:aws:iam::222222222222:oidc-provider/gitlab.com',
}
```
```bash
pnpm exec cdk deploy microvm-ci-oidc --profile client-a-account
```

Notes:
- An account can have only **one** OIDC provider per issuer URL. If gitlab.com is already registered in a target account, set `existingProviderArn` so the stack imports it and just adds the role.
- More projects in the same account → add more roles (or widen `oidcSubject`). Each role gets its own `sub` scope.
- The runner account needs **no** OIDC and **no** trust to the target accounts.

## Tear down

```bash
pnpm run destroy     # removes all CloudFormation resources
```

Two things live *outside* CloudFormation (the runner creates them at runtime): the **MicroVM image** and any **running MicroVMs**. To remove everything:

```bash
pnpm run teardown    # cdk destroy --all  +  scripts/cleanup-microvms.sh
```

For multi-account, destroy each stack with its own profile.

## What's in here

```
lib/config.ts              ← the only file you edit
lib/config-types.ts          its types (leave alone)
bin/app.ts                   CDK app; 2 stacks + tags via Tags.of(app)
lib/runner-stack.ts          runner account: bucket, build role, host
lib/gitlab-oidc-stack.ts     target account: OIDC provider + deploy role
lib/constructs/
  runner-host.ts             EC2 + IAM + user-data
  gitlab-oidc.ts             OIDC provider + deploy role construct
lib/user-data.ts + assets/user-data.sh   the host bootstrap script
assets/gitlab-ci-example.yml   sample pipeline (OIDC deploy)
scripts/cleanup-microvms.sh    removes the non-CFN bits
```

## Notes & cost

- The runner host is tiny and always-on (a few $/month); the real compute is pay-per-use MicroVMs, terminated at the end of each job.
- The deploy role ships with **demo** permissions (`s3:ListAllMyBuckets`) just to prove assumption — replace them in `lib/constructs/gitlab-oidc.ts` with what your deployments actually need.
- The MicroVM-driving IAM actions use the `lambda-microvms:` prefix (e.g. `lambda-microvms:RunMicrovm`) — the service's own namespace, matching `aws lambda-microvms` / `@aws-sdk/client-lambda-microvms`. Action names are the PascalCase of the documented CLI verbs. If a call returns `AccessDenied`, re-check the exact name in the [Service Authorization Reference](https://docs.aws.amazon.com/service-authorization/latest/reference/list_awslambdamicrovms.html).
