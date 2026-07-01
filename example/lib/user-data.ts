import { readFileSync } from 'fs';
import { join } from 'path';
import { Stack } from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import { IRole } from 'aws-cdk-lib/aws-iam';
import { IBucket } from 'aws-cdk-lib/aws-s3';
import { IConstruct } from 'constructs';
import { RunnerConfig } from './config-types';

/** Go toolchain the runner host installs to build the executor from source. */
const GO_VERSION = '1.26.4';

export interface UserDataInputs {
  /** Any construct in the stack — used to resolve the account id. */
  readonly scope: IConstruct;
  readonly config: RunnerConfig;
  readonly artifactBucket: IBucket;
  readonly buildRole: IRole;
}

/**
 * Builds the EC2 user-data that installs dependencies + the AWS CLI, builds the
 * executor from source, publishes the MicroVM image, and starts gitlab-runner.
 *
 * The bash lives in assets/user-data.sh; here we only substitute values. CDK
 * tokens (account, bucket name, role ARN) survive the substitution and resolve
 * at deploy time.
 */
export function buildUserData(inputs: UserDataInputs): ec2.UserData {
  const { config, artifactBucket, buildRole } = inputs;
  const account = Stack.of(inputs.scope).account;
  const arch = archMap(config.runner.cpuArch);
  const { region, gitlab, runner } = config;

  const template = readFileSync(join(__dirname, '..', 'assets', 'user-data.sh'), 'utf8');

  const script = replaceAll(template, {
    __REGION__: region,
    __BUCKET__: artifactBucket.bucketName,
    __BUILD_ROLE_ARN__: buildRole.roleArn,
    __BASE_IMAGE_ARN__: `arn:aws:lambda:${region}:aws:microvm-image:${runner.microvm.baseImage}`,
    __IMAGE_NAME__: runner.microvm.imageName,
    __IMAGE_ARN__: `arn:aws:lambda:${region}:${account}:microvm-image:${runner.microvm.imageName}`,
    __GITLAB_URL__: gitlab.url,
    __TOKEN_SSM_PARAM__: gitlab.runnerTokenSsmParameter,
    __CONCURRENT__: String(runner.concurrentJobs),
    __MAX_DURATION__: String(runner.microvm.maxDurationSeconds),
    __STATE_DIR__: '/var/lib/microvm-executor',
    __REPO_URL__: config.executorRepoUrl,
    __GO_VERSION__: GO_VERSION,
    __GO_ARCH__: arch.go,
    __RUNNER_ARCH__: arch.gitlabRunner,
    __AWSCLI_ARCH__: arch.awscli,
  });

  return ec2.UserData.custom(script);
}

/** Maps the configured CPU arch to the download flavours each tool uses. */
function archMap(cpu: RunnerConfig['runner']['cpuArch']) {
  return cpu === 'arm64'
    ? { go: 'linux-arm64', gitlabRunner: 'arm64', awscli: 'aarch64' }
    : { go: 'linux-amd64', gitlabRunner: 'amd64', awscli: 'x86_64' };
}

function replaceAll(template: string, values: Readonly<Record<string, string>>): string {
  return Object.entries(values).reduce(
    (acc, [placeholder, value]) => acc.split(placeholder).join(value),
    template,
  );
}
