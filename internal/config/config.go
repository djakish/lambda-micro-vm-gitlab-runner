// Package config loads driver configuration from the environment.
//
// Two layers feed the config:
//
//   - Runner-host variables (MICROVM_*), set on the gitlab-runner process,
//     define the defaults for every job this runner executes.
//   - Job variables, which GitLab exposes to custom-executor stages prefixed
//     with CUSTOM_ENV_. A job may override a small, safe subset (currently the
//     image and the VM size) via CUSTOM_ENV_MICROVM_*.
//
// Job overrides take precedence over runner-host defaults.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the fully-resolved driver configuration for a single job.
type Config struct {
	Region string

	// ImageARN identifies the MicroVM image to launch (ARN or image name the
	// aws CLI accepts as --image-identifier).
	ImageARN string

	IngressConnectors []string
	EgressConnectors  []string
	ExecutionRoleARN  string // optional

	IdlePolicyJSON     string // raw JSON passed to --idle-policy
	MaxDurationSeconds int    // 0 => omit (AWS default)

	// AgentPort is the port the in-VM agent listens on and the port the auth
	// token is scoped to.
	AgentPort int

	// Paths inside the MicroVM.
	BuildsDir string
	CacheDir  string

	// Operational knobs.
	AWSCLI               string
	StateDir             string
	RunTimeoutSeconds    int // wait for VM to reach RUNNING
	HealthTimeoutSeconds int // wait for the agent /healthz to answer
	TokenTTLMinutes      int // auth-token lifetime
}

// getenv is indirected so tests can supply a fake environment.
type getenv func(string) string

// Load resolves configuration from the real process environment.
func Load() (*Config, error) { return loadFrom(os.Getenv) }

func loadFrom(env getenv) (*Config, error) {
	region := first(env("MICROVM_REGION"), env("AWS_REGION"), env("AWS_DEFAULT_REGION"))
	if region == "" {
		return nil, fmt.Errorf("region not set: define MICROVM_REGION or AWS_REGION")
	}

	// Image: job override wins, else runner default.
	image := first(env("CUSTOM_ENV_MICROVM_IMAGE"), env("MICROVM_IMAGE_ARN"))
	if image == "" {
		return nil, fmt.Errorf("image not set: define MICROVM_IMAGE_ARN (or job var MICROVM_IMAGE)")
	}

	cfg := &Config{
		Region:               region,
		ImageARN:             image,
		IngressConnectors:    splitList(first(env("MICROVM_INGRESS_CONNECTORS"), defaultConnector(region, "ALL_INGRESS"))),
		EgressConnectors:     splitList(first(env("MICROVM_EGRESS_CONNECTORS"), defaultConnector(region, "INTERNET_EGRESS"))),
		ExecutionRoleARN:     env("MICROVM_EXECUTION_ROLE_ARN"),
		IdlePolicyJSON:       first(env("MICROVM_IDLE_POLICY"), defaultIdlePolicy),
		MaxDurationSeconds:   atoiDefault(env("MICROVM_MAX_DURATION_SECONDS"), 14400), // 4h
		AgentPort:            atoiDefault(first(env("CUSTOM_ENV_MICROVM_AGENT_PORT"), env("MICROVM_AGENT_PORT")), 8080),
		BuildsDir:            first(env("MICROVM_BUILDS_DIR"), "/builds"),
		CacheDir:             first(env("MICROVM_CACHE_DIR"), "/cache"),
		AWSCLI:               first(env("MICROVM_AWS_CLI"), "aws"),
		StateDir:             first(env("MICROVM_STATE_DIR"), "/tmp/microvm-executor"),
		RunTimeoutSeconds:    atoiDefault(env("MICROVM_RUN_TIMEOUT_SECONDS"), 180),
		HealthTimeoutSeconds: atoiDefault(env("MICROVM_HEALTH_TIMEOUT_SECONDS"), 120),
		TokenTTLMinutes:      atoiDefault(env("MICROVM_TOKEN_TTL_MINUTES"), tokenTTL(env)),
	}
	return cfg, nil
}

// tokenTTL defaults the auth-token lifetime to the job timeout plus a buffer so
// a single token covers all run sub-stages.
func tokenTTL(env getenv) int {
	if secs := atoiDefault(env("CUSTOM_ENV_CI_JOB_TIMEOUT"), 0); secs > 0 {
		return secs/60 + 10
	}
	return 70 // minutes; covers the default 1h job timeout with headroom
}

const defaultIdlePolicy = `{"autoResumeEnabled":true,"maxIdleDurationSeconds":900,"suspendedDurationSeconds":300}`

func defaultConnector(region, name string) string {
	return fmt.Sprintf("arn:aws:lambda:%s:aws:network-connector:aws-network-connector:%s", region, name)
}

func first(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
