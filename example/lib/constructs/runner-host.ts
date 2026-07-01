import { Stack } from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as iam from 'aws-cdk-lib/aws-iam';
import { IBucket } from 'aws-cdk-lib/aws-s3';
import { Construct } from 'constructs';
import { RunnerConfig } from '../config-types';
import { buildUserData } from '../user-data';

export interface RunnerHostProps {
  readonly config: RunnerConfig;
  /** Bucket the runner uploads the MicroVM image build artifact to. */
  readonly artifactBucket: IBucket;
  /** IAM role Lambda assumes to build the MicroVM image (passed by the host). */
  readonly buildRole: iam.IRole;
}

/**
 * The always-on orchestrator: a small EC2 instance running gitlab-runner + the
 * MicroVM custom executor. It does no build work itself — it just launches a
 * MicroVM per job. IMDSv2-only, encrypted root volume, egress-only, and
 * reachable via SSM Session Manager (no SSH keys, no inbound ports).
 */
export class RunnerHost extends Construct {
  public readonly instance: ec2.Instance;

  constructor(scope: Construct, id: string, props: RunnerHostProps) {
    super(scope, id);
    const { config } = props;

    // A minimal single-AZ VPC with one public subnet and no NAT gateway — the
    // host needs egress (GitLab, AWS APIs, package mirrors) but no inbound.
    const vpc = new ec2.Vpc(this, 'Vpc', {
      maxAzs: 1,
      natGateways: 0,
      subnetConfiguration: [{ name: 'public', subnetType: ec2.SubnetType.PUBLIC, cidrMask: 24 }],
      restrictDefaultSecurityGroup: true,
    });

    const role = new iam.Role(this, 'InstanceRole', {
      description: 'MicroVM GitLab runner host',
      assumedBy: new iam.ServicePrincipal('ec2.amazonaws.com'),
      // Session Manager access — connect without SSH keys or open ports.
      managedPolicies: [iam.ManagedPolicy.fromAwsManagedPolicyName('AmazonSSMManagedInstanceCore')],
    });
    this.grantRunnerPermissions(role, props);

    const securityGroup = new ec2.SecurityGroup(this, 'SecurityGroup', {
      vpc,
      allowAllOutbound: true,
      description: 'MicroVM runner host - egress only, no inbound',
    });

    this.instance = new ec2.Instance(this, 'Instance', {
      vpc,
      vpcSubnets: { subnetType: ec2.SubnetType.PUBLIC },
      instanceType: new ec2.InstanceType(config.runner.instanceType),
      machineImage: ec2.MachineImage.latestAmazonLinux2023({ cpuType: cpuTypeFor(config.runner.cpuArch) }),
      role,
      securityGroup,
      requireImdsv2: true,
      blockDevices: [
        {
          deviceName: '/dev/xvda',
          volume: ec2.BlockDeviceVolume.ebs(20, {
            encrypted: true,
            volumeType: ec2.EbsDeviceVolumeType.GP3,
          }),
        },
      ],
      userData: buildUserData({
        scope: this,
        config,
        artifactBucket: props.artifactBucket,
        buildRole: props.buildRole,
      }),
    });
  }

  /** Grants the host everything it needs: drive MicroVMs, build the image, read the token. */
  private grantRunnerPermissions(role: iam.Role, props: RunnerHostProps): void {
    const stack = Stack.of(this);
    const { config, artifactBucket, buildRole } = props;

    // Drive MicroVMs. The IAM prefix is `lambda-microvms:` (the service's own
    // namespace — the same string used by `aws lambda-microvms`, boto3
    // `client("lambda-microvms")`, and `@aws-sdk/client-lambda-microvms`), NOT
    // the `lambda:` prefix of regular Lambda functions. Action names are the
    // PascalCase of the documented CLI verbs.
    role.addToPolicy(
      new iam.PolicyStatement({
        sid: 'DriveMicrovms',
        actions: [
          'lambda-microvms:RunMicrovm',
          'lambda-microvms:GetMicrovm',
          'lambda-microvms:ListMicrovms',
          'lambda-microvms:CreateMicrovmAuthToken',
          'lambda-microvms:SuspendMicrovm',
          'lambda-microvms:ResumeMicrovm',
          'lambda-microvms:TerminateMicrovm',
        ],
        resources: ['*'],
      }),
    );

    // Build + register the MicroVM image on first boot.
    role.addToPolicy(
      new iam.PolicyStatement({
        sid: 'ManageMicrovmImages',
        actions: ['lambda-microvms:CreateMicrovmImage', 'lambda-microvms:GetMicrovmImage'],
        resources: ['*'],
      }),
    );
    artifactBucket.grantReadWrite(role);
    buildRole.grantPassRole(role);

    // Read the GitLab runner token from its SSM SecureString.
    role.addToPolicy(
      new iam.PolicyStatement({
        sid: 'ReadRunnerToken',
        actions: ['ssm:GetParameter'],
        resources: [
          stack.formatArn({
            service: 'ssm',
            resource: 'parameter',
            resourceName: config.gitlab.runnerTokenSsmParameter.replace(/^\//, ''),
          }),
        ],
      }),
    );
  }
}

function cpuTypeFor(cpuArch: RunnerConfig['runner']['cpuArch']): ec2.AmazonLinuxCpuType {
  return cpuArch === 'arm64' ? ec2.AmazonLinuxCpuType.ARM_64 : ec2.AmazonLinuxCpuType.X86_64;
}
