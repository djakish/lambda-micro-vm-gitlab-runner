// Package microvm is a thin wrapper over the `aws lambda-microvms` CLI.
//
// Lambda MicroVMs is a new service; rather than depend on an SDK build that may
// not yet ship the client, the driver shells out to the AWS CLI. This also
// means credential resolution (instance profile, SSO, env vars) is handled by
// the standard AWS toolchain already present on a runner host.
package microvm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// Client invokes the AWS CLI. Zero value is not usable; use New.
type Client struct {
	cli    string // path to the aws binary
	region string
}

// New returns a client bound to a CLI path and region.
func New(cli, region string) *Client { return &Client{cli: cli, region: region} }

// RunInput describes a run-microvm call.
type RunInput struct {
	ImageIdentifier    string
	IngressConnectors  []string
	EgressConnectors   []string
	ExecutionRoleARN   string
	IdlePolicyJSON     string
	MaxDurationSeconds int
}

// RunResult is the subset of run-microvm output the driver needs.
type RunResult struct {
	MicrovmID string `json:"microvmId"`
	State     string `json:"state"`
	Endpoint  string `json:"endpoint"`
}

// RunMicrovm launches a MicroVM from an image.
func (c *Client) RunMicrovm(ctx context.Context, in RunInput) (*RunResult, error) {
	args := []string{"run-microvm", "--image-identifier", in.ImageIdentifier}
	for _, arn := range in.IngressConnectors {
		args = append(args, "--ingress-network-connectors", arn)
	}
	for _, arn := range in.EgressConnectors {
		args = append(args, "--egress-network-connectors", arn)
	}
	if in.ExecutionRoleARN != "" {
		args = append(args, "--execution-role-arn", in.ExecutionRoleARN)
	}
	if in.IdlePolicyJSON != "" {
		args = append(args, "--idle-policy", in.IdlePolicyJSON)
	}
	if in.MaxDurationSeconds > 0 {
		args = append(args, "--maximum-duration-in-seconds", strconv.Itoa(in.MaxDurationSeconds))
	}
	var out RunResult
	if err := c.runJSON(ctx, &out, args...); err != nil {
		return nil, err
	}
	if out.MicrovmID == "" {
		return nil, fmt.Errorf("run-microvm returned no microvmId")
	}
	return &out, nil
}

// GetState returns the current lifecycle state of a MicroVM.
func (c *Client) GetState(ctx context.Context, id string) (string, error) {
	var out struct {
		State string `json:"state"`
	}
	if err := c.runJSON(ctx, &out, "get-microvm", "--microvm-identifier", id); err != nil {
		return "", err
	}
	return out.State, nil
}

// WaitRunning polls get-microvm until the VM is RUNNING or a terminal/failed
// state is reached or the timeout elapses.
func (c *Client) WaitRunning(ctx context.Context, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := c.GetState(ctx, id)
		if err != nil {
			return err
		}
		switch st {
		case "RUNNING":
			return nil
		case "FAILED", "TERMINATED", "TERMINATING":
			return fmt.Errorf("microvm %s entered terminal state %q while waiting for RUNNING", id, st)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for microvm %s to reach RUNNING (last state %q)", timeout, id, st)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// PortSpec is one entry of the create-microvm-auth-token --allowed-ports list.
type PortSpec struct {
	Port int `json:"port"`
}

// CreateAuthToken mints a JWE token scoped to a single port.
func (c *Client) CreateAuthToken(ctx context.Context, id string, ttlMinutes, port int) (string, error) {
	allowed, err := json.Marshal([]PortSpec{{Port: port}})
	if err != nil {
		return "", err
	}
	var out struct {
		AuthToken map[string]string `json:"authToken"`
	}
	if err := c.runJSON(ctx, &out,
		"create-microvm-auth-token",
		"--microvm-identifier", id,
		"--expiration-in-minutes", strconv.Itoa(ttlMinutes),
		"--allowed-ports", string(allowed),
	); err != nil {
		return "", err
	}
	tok := out.AuthToken["X-aws-proxy-auth"]
	if tok == "" {
		return "", fmt.Errorf("create-microvm-auth-token returned no X-aws-proxy-auth value")
	}
	return tok, nil
}

// Terminate stops a MicroVM and releases its resources. Idempotent enough for
// cleanup: a not-found VM is reported as an error the caller may ignore.
func (c *Client) Terminate(ctx context.Context, id string) error {
	return c.run(ctx, "terminate-microvm", "--microvm-identifier", id)
}

// runJSON runs an aws lambda-microvms subcommand and decodes stdout as JSON.
func (c *Client) runJSON(ctx context.Context, dst any, args ...string) error {
	stdout, err := c.exec(ctx, args...)
	if err != nil {
		return err
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(stdout, dst); err != nil {
		return fmt.Errorf("parse `aws %s` output: %w", args[0], err)
	}
	return nil
}

// run runs a subcommand and discards its output.
func (c *Client) run(ctx context.Context, args ...string) error {
	_, err := c.exec(ctx, args...)
	return err
}

func (c *Client) exec(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"lambda-microvms"}, args...)
	full = append(full, "--output", "json")
	if c.region != "" {
		full = append(full, "--region", c.region)
	}
	cmd := exec.CommandContext(ctx, c.cli, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("aws %s failed: %w: %s", args[0], err, trim(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func trim(s string) string {
	const max = 2000
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
