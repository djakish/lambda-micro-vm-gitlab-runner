package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Options tweak the wizard's behavior.
type Options struct {
	AssumeYes bool // accept all confirmations with their defaults
	NoColor   bool // disable ANSI styling
}

// Run executes the interactive setup wizard. It returns an error only for fatal
// problems; user-driven "skip" choices are handled inline.
func Run(opts Options) error {
	color := !opts.NoColor && isTerminal(os.Stdout)
	p := newPrompter(os.Stdin, os.Stdout, color, opts.AssumeYes)

	fmt.Fprintf(p.out, "\n%s┌─ GitLab Runner · AWS Lambda MicroVM executor ─┐%s\n", p.s.bold, p.s.reset)
	fmt.Fprintf(p.out, "%s└─ interactive setup                            ┘%s\n", p.s.bold, p.s.reset)

	// --- Preflight ----------------------------------------------------------
	p.section("1/5  Preflight")
	cliPath, err := exec.LookPath("aws")
	if err != nil {
		p.fail("AWS CLI not found on PATH. Install AWS CLI v2 (with lambda-microvms) first.")
		return fmt.Errorf("aws cli not found")
	}
	p.good("found AWS CLI: %s", cliPath)

	region := firstNonEmpty(os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"), awsc{cli: "aws"}.configuredRegion())
	region = p.askRequired("AWS region", region)
	aws := awsc{cli: "aws", region: region}

	id, err := aws.callerIdentity()
	if err != nil {
		p.fail("could not verify AWS credentials: %v", err)
		return fmt.Errorf("aws credentials not usable: %w", err)
	}
	p.good("authenticated as %s (account %s)", id.Arn, id.Account)

	// --- Image --------------------------------------------------------------
	p.section("2/5  MicroVM image")
	var imageARN string
	if p.confirm("Build & publish a new MicroVM image now?", true) {
		imageARN, err = buildImage(p, aws, region, id.Account)
		if err != nil {
			return err
		}
	} else {
		imageARN = p.askRequired("Existing MicroVM image ARN", "")
	}
	p.good("using image: %s", imageARN)

	// --- Runner config ------------------------------------------------------
	p.section("3/5  GitLab Runner")
	gitlabURL := p.askRequired("GitLab URL", "https://gitlab.com/")
	token := p.secret("Runner authentication token")
	if token == "" {
		p.warn("no token entered; leaving a placeholder in the config")
		token = "REPLACE_WITH_RUNNER_AUTH_TOKEN"
	}

	p.section("4/5  Paths & limits")
	answers := configAnswers{
		Region:      region,
		ImageARN:    imageARN,
		GitLabURL:   gitlabURL,
		Token:       token,
		BuildsDir:   p.ask("Builds dir (inside the MicroVM)", "/builds"),
		CacheDir:    p.ask("Cache dir (inside the MicroVM)", "/cache"),
		StateDir:    p.ask("State dir (on the runner host)", "/var/lib/microvm-executor"),
		MaxDuration: p.askInt("Max MicroVM duration (seconds)", 14400),
		InstallPath: p.ask("Driver install path", "/opt/microvm-executor/bin/microvm-executor"),
		Concurrent:  p.askInt("Concurrent jobs (VMs at once)", 10),
	}
	configOut := p.ask("Write config.toml to", "./config.toml")

	// --- Apply --------------------------------------------------------------
	p.section("5/5  Generate & install")
	if err := os.WriteFile(configOut, []byte(renderConfig(answers)), 0o600); err != nil {
		p.fail("could not write %s: %v", configOut, err)
		return err
	}
	p.good("wrote %s", configOut)

	if p.confirm(fmt.Sprintf("Install this binary to %s?", answers.InstallPath), false) {
		installBinary(p, answers.InstallPath)
	}

	printNextSteps(p, answers, configOut, id.Account)
	return nil
}

// buildImage runs the full publish flow and returns the resulting image ARN.
func buildImage(p *prompter, aws awsc, region, account string) (string, error) {
	repoRoot := findRepoRoot(mustGetwd())
	if repoRoot == "" {
		p.fail("could not locate the repo (need go.mod + image/Dockerfile).")
		p.info("Run this from a checkout of the executor repo, or choose 'no' and pass an existing image ARN.")
		return "", fmt.Errorf("repo root not found for image build")
	}
	p.good("repo: %s", repoRoot)

	imageName := p.ask("Image name", "gitlab-ci-runner")
	baseARN := p.ask("Base image ARN", fmt.Sprintf("arn:aws:lambda:%s:aws:microvm-image:al2023-1", region))
	bucket := p.askRequired("S3 bucket for the build artifact", fmt.Sprintf("microvm-ci-%s-%s", account, region))

	// S3 bucket
	if aws.bucketExists(bucket) {
		p.good("bucket s3://%s exists", bucket)
	} else if p.confirm(fmt.Sprintf("Bucket s3://%s does not exist. Create it?", bucket), true) {
		if err := aws.createBucket(bucket); err != nil {
			p.fail("create bucket: %v", err)
			return "", err
		}
		p.good("created s3://%s", bucket)
	} else {
		return "", fmt.Errorf("bucket %s is required", bucket)
	}

	// Build role
	roleName := p.ask("IAM build role name", "MicrovmBuildRole")
	roleARN, err := ensureBuildRole(p, aws, roleName, bucket)
	if err != nil {
		return "", err
	}

	// Assemble + upload artifact
	p.info("packaging build context…")
	zipPath, err := zipContext(repoRoot)
	if err != nil {
		p.fail("zip build context: %v", err)
		return "", err
	}
	defer os.Remove(zipPath)
	s3uri := fmt.Sprintf("s3://%s/microvm-images/%s.zip", bucket, imageName)
	if err := aws.uploadArtifact(zipPath, s3uri); err != nil {
		p.fail("upload artifact: %v", err)
		return "", err
	}
	p.good("uploaded artifact to %s", s3uri)

	// Create image + poll
	p.info("creating MicroVM image (Lambda builds the Dockerfile)…")
	if err := aws.createMicrovmImage(imageName, s3uri, baseARN, roleARN); err != nil {
		p.fail("create-microvm-image: %v", err)
		return "", err
	}
	arn, err := pollImage(p, aws, imageName)
	if err != nil {
		return "", err
	}
	p.good("image built: %s", arn)
	return arn, nil
}

func ensureBuildRole(p *prompter, aws awsc, roleName, bucket string) (string, error) {
	if aws.roleExists(roleName) {
		p.good("IAM role %s exists", roleName)
	} else if p.confirm(fmt.Sprintf("IAM role %s does not exist. Create it?", roleName), true) {
		if err := aws.createRole(roleName, buildRoleTrust); err != nil {
			p.fail("create role: %v", err)
			return "", err
		}
		if err := aws.putRolePolicy(roleName, "microvm-build", fmt.Sprintf(buildRolePolicy, bucket)); err != nil {
			p.fail("attach role policy: %v", err)
			return "", err
		}
		p.good("created role %s with S3 + logs permissions", roleName)
	} else {
		return "", fmt.Errorf("build role %s is required", roleName)
	}
	return aws.roleARN(roleName)
}

func pollImage(p *prompter, aws awsc, name string) (string, error) {
	deadline := time.Now().Add(15 * time.Minute)
	for {
		state, arn, err := aws.microvmImage(name)
		if err != nil {
			return "", fmt.Errorf("poll image: %w", err)
		}
		switch state {
		case "CREATED":
			return arn, nil
		case "CREATE_FAILED":
			return "", fmt.Errorf("image build failed; check CloudWatch logs /aws/lambda/microvms/%s", name)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for image %s (last state %s)", name, state)
		}
		p.info("  building… state=%s", state)
		time.Sleep(10 * time.Second)
	}
}

func installBinary(p *prompter, dest string) {
	self, err := os.Executable()
	if err != nil {
		p.warn("cannot locate this binary: %v", err)
		return
	}
	if err := copyExecutable(self, dest); err != nil {
		p.warn("could not install (permissions?): %v", err)
		p.info("Run manually:  sudo install -D -m 0755 %s %s", self, dest)
		return
	}
	p.good("installed driver to %s", dest)
}

func copyExecutable(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, in, 0o755)
}

func printNextSteps(p *prompter, a configAnswers, configOut, account string) {
	p.section("Done 🎉")
	p.info("Next steps:")
	p.info("  1. Attach the runner-host IAM policy (deploy/iam/runner-host-policy.json)")
	p.info("     to the runner's instance profile so it can drive MicroVMs.")
	if !strings.HasPrefix(configOut, "/etc/gitlab-runner/") {
		p.info("  2. Move the config into place:")
		p.info("       sudo cp %s /etc/gitlab-runner/config.toml", configOut)
		p.info("  3. Restart the runner:  sudo gitlab-runner restart")
	} else {
		p.info("  2. Restart the runner:  sudo gitlab-runner restart")
	}
	p.info("")
	p.info("Then push any pipeline — each job runs in its own Lambda MicroVM.")
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
