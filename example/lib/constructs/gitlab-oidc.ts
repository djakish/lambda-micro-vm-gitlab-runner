import { Duration } from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';
import { Construct } from 'constructs';

export interface GitlabOidcProps {
  /** OIDC issuer (GitLab instance URL, no trailing slash). */
  readonly issuer: string;
  /** Audience (`aud`) claim the CI job requests and this provider trusts. */
  readonly audience: string;
  /** `sub` claim pattern (StringLike) restricting which jobs may assume the role. */
  readonly subject: string;
  /** Optional fixed name for the deploy role (referenced from .gitlab-ci.yml). */
  readonly deployRoleName?: string;
  /**
   * Reuse an existing IAM OIDC provider ARN instead of creating one. An account
   * may only have one provider per issuer URL.
   */
  readonly existingProviderArn?: string;
}

/**
 * Registers GitLab as an IAM OIDC identity provider and creates a deploy role
 * that GitLab CI jobs assume via web identity — so pipelines never need static
 * AWS access keys.
 */
export class GitlabOidc extends Construct {
  public readonly provider: iam.IOpenIdConnectProvider;
  public readonly deployRole: iam.Role;

  constructor(scope: Construct, id: string, props: GitlabOidcProps) {
    super(scope, id);

    // Condition keys are prefixed with the issuer host, e.g. "gitlab.com:sub".
    const host = new URL(props.issuer).host;

    this.provider = props.existingProviderArn
      ? iam.OpenIdConnectProvider.fromOpenIdConnectProviderArn(this, 'Provider', props.existingProviderArn)
      : new iam.OpenIdConnectProvider(this, 'Provider', {
          url: props.issuer,
          clientIds: [props.audience],
        });

    this.deployRole = new iam.Role(this, 'DeployRole', {
      roleName: props.deployRoleName,
      description: 'Assumed by GitLab CI jobs via OIDC — no static AWS keys.',
      maxSessionDuration: Duration.hours(1),
      assumedBy: new iam.OpenIdConnectPrincipal(this.provider, {
        StringEquals: { [`${host}:aud`]: props.audience },
        StringLike: { [`${host}:sub`]: props.subject },
      }),
    });

    // --- DEMO permissions -------------------------------------------------
    // Enough for the sample job to prove it assumed the role. Replace with the
    // real permissions your deployments need (least privilege).
    this.deployRole.addToPolicy(
      new iam.PolicyStatement({
        sid: 'DemoProveAssumption',
        actions: ['s3:ListAllMyBuckets'],
        resources: ['*'],
      }),
    );
  }
}
