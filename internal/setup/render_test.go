package setup

import (
	"strings"
	"testing"
)

func TestRenderConfig(t *testing.T) {
	got := renderConfig(configAnswers{
		Region:      "eu-west-1",
		ImageARN:    "arn:aws:lambda:eu-west-1:123:microvm-image:ci",
		GitLabURL:   "https://gitlab.example.com/",
		Token:       "glrt-secret",
		BuildsDir:   "/builds",
		CacheDir:    "/cache",
		StateDir:    "/var/lib/microvm-executor",
		MaxDuration: 7200,
		InstallPath: "/opt/microvm-executor/bin/microvm-executor",
		Concurrent:  5,
	})

	for _, want := range []string{
		"concurrent = 5",
		`url = "https://gitlab.example.com/"`,
		`token = "glrt-secret"`,
		"MICROVM_REGION=eu-west-1",
		"MICROVM_IMAGE_ARN=arn:aws:lambda:eu-west-1:123:microvm-image:ci",
		"MICROVM_MAX_DURATION_SECONDS=7200",
		"MICROVM_STATE_DIR=/var/lib/microvm-executor",
		`run_args = ["run"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered config missing %q\n---\n%s", want, got)
		}
	}

	// The install path is used for all four exec stages.
	if n := strings.Count(got, "/opt/microvm-executor/bin/microvm-executor"); n != 4 {
		t.Errorf("install path appears %d times, want 4 (config/prepare/run/cleanup)", n)
	}
}

func TestBuildRolePolicySubstitution(t *testing.T) {
	pol := strings.Replace(buildRolePolicy, "%s", "my-bucket", 1)
	if !strings.Contains(pol, "arn:aws:s3:::my-bucket/*") {
		t.Errorf("bucket not substituted into build role policy:\n%s", pol)
	}
}
