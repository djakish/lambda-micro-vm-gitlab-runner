import { CfnOutput, Stack, StackProps } from 'aws-cdk-lib';
import { Construct } from 'constructs';
import { RunnerConfig } from './config-types';
import { GitlabOidc } from './constructs/gitlab-oidc';

export interface GitlabOidcStackProps extends StackProps {
  readonly config: RunnerConfig;
}

/**
 * GitLab OIDC provider + deploy role. Deploy this into each account your
 * pipelines deploy TO — which is usually NOT the runner account. It is fully
 * independent of the runner stack: the CI job assumes this role directly from
 * inside the MicroVM (job -> this account's STS), so the runner account is never
 * in the path.
 */
export class GitlabOidcStack extends Stack {
  constructor(scope: Construct, id: string, props: GitlabOidcStackProps) {
    super(scope, id, props);
    const { deployRole, project } = props.config;

    const oidc = new GitlabOidc(this, 'GitlabOidc', {
      issuer: deployRole.oidcIssuer,
      audience: deployRole.oidcAudience,
      subject: deployRole.oidcSubject,
      deployRoleName: deployRole.roleName ?? `${project}-deploy`,
      existingProviderArn: deployRole.existingProviderArn,
    });

    new CfnOutput(this, 'DeployRoleArn', {
      value: oidc.deployRole.roleArn,
      description: 'Set this as AWS_ROLE_ARN in your .gitlab-ci.yml deploy job',
    });
  }
}
