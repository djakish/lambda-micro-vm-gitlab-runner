import { CfnOutput, RemovalPolicy, Stack, StackProps } from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as s3 from 'aws-cdk-lib/aws-s3';
import { Construct } from 'constructs';
import { RunnerConfig } from './config-types';
import { RunnerHost } from './constructs/runner-host';

export interface RunnerStackProps extends StackProps {
  readonly config: RunnerConfig;
}

/**
 * The runner-account stack: an S3 bucket for the image build artifact, the IAM
 * role Lambda assumes to build the image, and the always-on runner host that
 * installs and runs the executor. No OIDC here — that lives with your deploy
 * target(s), see GitlabOidcStack.
 *
 * Everything is CloudFormation-managed, so `cdk destroy` removes it cleanly.
 */
export class RunnerStack extends Stack {
  constructor(scope: Construct, id: string, props: RunnerStackProps) {
    super(scope, id, props);
    const { config } = props;

    const artifactBucket = new s3.Bucket(this, 'ArtifactBucket', {
      encryption: s3.BucketEncryption.S3_MANAGED,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
      removalPolicy: RemovalPolicy.DESTROY,
      autoDeleteObjects: true,
    });

    const buildRole = new iam.Role(this, 'ImageBuildRole', {
      description: 'Lambda MicroVM image build role',
      assumedBy: new iam.ServicePrincipal('lambda.amazonaws.com'),
    });
    // Match the service trust policy (assume + tag session).
    buildRole.assumeRolePolicy?.addStatements(
      new iam.PolicyStatement({
        actions: ['sts:TagSession'],
        principals: [new iam.ServicePrincipal('lambda.amazonaws.com')],
      }),
    );
    artifactBucket.grantRead(buildRole);
    buildRole.addToPolicy(
      new iam.PolicyStatement({
        sid: 'BuildLogs',
        actions: ['logs:CreateLogGroup', 'logs:CreateLogStream', 'logs:PutLogEvents'],
        resources: ['arn:aws:logs:*:*:*'],
      }),
    );

    const host = new RunnerHost(this, 'RunnerHost', {
      config,
      artifactBucket,
      buildRole,
    });

    new CfnOutput(this, 'RunnerInstanceId', {
      value: host.instance.instanceId,
      description: 'EC2 instance running gitlab-runner + the MicroVM executor',
    });
    new CfnOutput(this, 'ArtifactBucketName', {
      value: artifactBucket.bucketName,
    });
    new CfnOutput(this, 'ConnectCommand', {
      value: `aws ssm start-session --target ${host.instance.instanceId} --region ${this.region}`,
      description: 'Open a shell on the runner host (no SSH key needed)',
    });
  }
}
