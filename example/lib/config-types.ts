/**
 * Type definitions for the deployment config.
 *
 * You don't need to touch this file — just edit the values in `config.ts`.
 * It's here so `config.ts` stays a clean list of settings.
 */

export interface RunnerConfig {
  /** Short slug used to name and tag every resource. */
  readonly project: string;
  /** AWS region the runner host is deployed into. */
  readonly region: string;

  readonly gitlab: GitlabConfig;
  readonly runner: RunnerHostConfig;
  /** Keyless CI deploys via GitLab OIDC (a separate, optional stack). */
  readonly deployRole: DeployRoleConfig;

  /** Git URL of the executor repo the runner host builds from on first boot. */
  readonly executorRepoUrl: string;
}

export interface GitlabConfig {
  /** GitLab base URL the runner registers against. */
  readonly url: string;
  /** Name of the SSM SecureString parameter holding the runner token. */
  readonly runnerTokenSsmParameter: string;
}

export interface RunnerHostConfig {
  /** EC2 instance type for the (orchestrator-only) runner host. Keep it small. */
  readonly instanceType: string;
  /** CPU architecture of that instance type — drives the AMI and downloads. */
  readonly cpuArch: 'arm64' | 'x86_64';
  /** Max concurrent jobs = max MicroVMs running at once. */
  readonly concurrentJobs: number;

  readonly microvm: MicrovmConfig;
}

export interface MicrovmConfig {
  /** Name to register the MicroVM image under (built on first boot). */
  readonly imageName: string;
  /** Lambda-managed base image identifier, e.g. "al2023-1". */
  readonly baseImage: string;
  /** Hard cap on a MicroVM's lifetime in seconds (1–28800). Backstop against leaks. */
  readonly maxDurationSeconds: number;
}

/**
 * GitLab OIDC provider + deploy role. This is what lets CI jobs assume an AWS
 * role with no static keys. IMPORTANT: it belongs in the account you DEPLOY TO,
 * which may not be the runner account — see the multi-account notes in the README.
 */
export interface DeployRoleConfig {
  /** Create the OIDC stack in this app at all? false = deploy only the EC2 runner. */
  readonly enabled: boolean;
  /** Target account for the OIDC stack. Defaults to the runner account. */
  readonly account?: string;
  /** Target region for the OIDC stack. Defaults to the runner region. */
  readonly region?: string;

  /** OIDC issuer (GitLab instance URL, no trailing slash). */
  readonly oidcIssuer: string;
  /** Audience (`aud`) the CI job requests and this provider trusts. */
  readonly oidcAudience: string;
  /** Which GitLab jobs may assume the role (matched against `sub`, StringLike). */
  readonly oidcSubject: string;

  /** Optional fixed deploy-role name (referenced from .gitlab-ci.yml). */
  readonly roleName?: string;
  /**
   * Reuse an existing IAM OIDC provider instead of creating one. An account can
   * only have ONE provider per issuer URL, so set this if gitlab.com is already
   * registered in the target account.
   */
  readonly existingProviderArn?: string;
}
