import { RunnerConfig } from './config-types';

// ┌──────────────────────────────────────────────────────────────────────────────┐
// │  This is the ONLY file you edit.                                              │
// │  Change the lines marked  ← change me , then run:  pnpm install && pnpm deploy │
// │  On gitlab.com? You really only need to set `oidcSubject` to your project.     │
// └──────────────────────────────────────────────────────────────────────────────┘

export const config: RunnerConfig = {
  project: 'microvm-ci',
  region: 'us-east-1', //                                          ← change me (your AWS region)

  gitlab: {
    url: 'https://gitlab.com/', //                                 ← change me if self-managed
    // Name of the SSM parameter you create to hold the runner token (see README).
    runnerTokenSsmParameter: '/microvm-ci/gitlab-token',
  },

  runner: {
    instanceType: 't4g.small', // small + cheap; it only orchestrates
    cpuArch: 'arm64', //         match the instance type (t4g = arm64, t3 = x86_64)
    concurrentJobs: 4, //        max jobs (= MicroVMs) at once

    microvm: {
      imageName: 'gitlab-ci-runner',
      baseImage: 'al2023-1',
      maxDurationSeconds: 14400, // 4h safety cap per VM
    },
  },

  // Keyless CI deploys via GitLab OIDC. This provider + role live in the account
  // you DEPLOY TO — which may not be the runner account. See "multi-account" in
  // the README. Set enabled:false to deploy just the EC2 runner and nothing else.
  deployRole: {
    enabled: true, //                                              ← false = EC2 runner only
    // account: '222222222222', // target account (default: the runner account)
    // existingProviderArn: 'arn:aws:iam::222222222222:oidc-provider/gitlab.com',

    // Using gitlab.com? Leave issuer/audience as-is. Self-managed: your instance URL.
    oidcIssuer: 'https://gitlab.com',
    oidcAudience: 'https://gitlab.com',
    // Who may deploy. Put YOUR group/project here. `ref:*` means "any branch".
    oidcSubject: 'project_path:my-group/my-project:ref_type:branch:ref:*', // ← change me
  },

  executorRepoUrl: 'https://github.com/djakish/lambda-micro-vm-gitlab-runner.git',
};
