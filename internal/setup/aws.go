package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// awsc runs AWS CLI commands for the wizard.
type awsc struct {
	cli    string
	region string
}

func (a awsc) run(args ...string) ([]byte, error) {
	if a.region != "" {
		args = append(args, "--region", a.region)
	}
	cmd := exec.Command(a.cli, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// Identity holds the resolved AWS caller identity.
type Identity struct {
	Account string `json:"Account"`
	Arn     string `json:"Arn"`
}

func (a awsc) callerIdentity() (Identity, error) {
	out, err := a.run("sts", "get-caller-identity", "--output", "json")
	if err != nil {
		return Identity{}, err
	}
	var id Identity
	return id, json.Unmarshal(out, &id)
}

// configuredRegion returns the region from the AWS config/profile, if any.
func (a awsc) configuredRegion() string {
	out, err := exec.Command(a.cli, "configure", "get", "region").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (a awsc) bucketExists(bucket string) bool {
	_, err := a.run("s3api", "head-bucket", "--bucket", bucket)
	return err == nil
}

func (a awsc) createBucket(bucket string) error {
	// `s3 mb` handles the us-east-1 LocationConstraint special case for us.
	_, err := a.run("s3", "mb", "s3://"+bucket)
	return err
}

func (a awsc) roleExists(name string) bool {
	_, err := a.run("iam", "get-role", "--role-name", name)
	return err == nil
}

func (a awsc) roleARN(name string) (string, error) {
	out, err := a.run("iam", "get-role", "--role-name", name, "--output", "json")
	if err != nil {
		return "", err
	}
	var resp struct {
		Role struct {
			Arn string `json:"Arn"`
		} `json:"Role"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", err
	}
	return resp.Role.Arn, nil
}

func (a awsc) createRole(name, trustJSON string) error {
	_, err := a.run("iam", "create-role",
		"--role-name", name,
		"--assume-role-policy-document", trustJSON,
	)
	return err
}

func (a awsc) putRolePolicy(role, policy, docJSON string) error {
	_, err := a.run("iam", "put-role-policy",
		"--role-name", role,
		"--policy-name", policy,
		"--policy-document", docJSON,
	)
	return err
}

func (a awsc) uploadArtifact(localPath, s3uri string) error {
	_, err := a.run("s3", "cp", localPath, s3uri)
	return err
}

func (a awsc) createMicrovmImage(name, s3uri, baseARN, buildRoleARN string) error {
	_, err := a.run("lambda-microvms", "create-microvm-image",
		"--name", name,
		"--code-artifact", "uri="+s3uri,
		"--base-image-arn", baseARN,
		"--build-role-arn", buildRoleARN,
		"--output", "json",
	)
	return err
}

// microvmImage returns the (state, arn) of a MicroVM image by name.
func (a awsc) microvmImage(name string) (state, arn string, err error) {
	out, err := a.run("lambda-microvms", "get-microvm-image",
		"--image-identifier", name, "--output", "json")
	if err != nil {
		return "", "", err
	}
	var resp struct {
		State    string `json:"state"`
		ImageArn string `json:"imageArn"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", "", err
	}
	return resp.State, resp.ImageArn, nil
}
