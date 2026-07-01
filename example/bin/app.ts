#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import { config } from '../lib/config';
import { GitlabOidcStack } from '../lib/gitlab-oidc-stack';
import { RunnerStack } from '../lib/runner-stack';

const app = new cdk.App();
const runnerAccount = process.env.CDK_DEFAULT_ACCOUNT;

// The runner host — deploy once, in the account where your runners live.
new RunnerStack(app, `${config.project}-runner`, {
  config,
  env: { account: runnerAccount, region: config.region },
  description: 'AWS Lambda MicroVM GitLab runner host (quickstart)',
});

// The GitLab OIDC provider + deploy role. Optional, and usually deployed into a
// DIFFERENT (target) account than the runners — see the README multi-account
// notes. Set deployRole.enabled = false to deploy just the EC2 runner.
if (config.deployRole.enabled) {
  new GitlabOidcStack(app, `${config.project}-oidc`, {
    config,
    env: {
      account: config.deployRole.account ?? runnerAccount,
      region: config.deployRole.region ?? config.region,
    },
    description: 'GitLab OIDC provider + deploy role (deploy per target account)',
  });
}

// Consistent tags on every resource. `Tags.of(...)` is CDK's built-in tagging
// Aspect — it propagates down the whole construct tree.
// https://docs.aws.amazon.com/cdk/v2/guide/tagging.html
cdk.Tags.of(app).add('Project', config.project);
cdk.Tags.of(app).add('ManagedBy', 'cdk');
cdk.Tags.of(app).add('Example', 'microvm-gitlab-runner');
