package state

import (
	"os"
	"testing"
)

func TestKey(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"project and job", map[string]string{"CUSTOM_ENV_CI_PROJECT_ID": "42", "CUSTOM_ENV_CI_JOB_ID": "1001"}, "42-1001"},
		{"job only", map[string]string{"CUSTOM_ENV_CI_JOB_ID": "1001"}, "1001"},
		{"concurrent fallback", map[string]string{"CUSTOM_ENV_CI_CONCURRENT_ID": "3"}, "job-3"},
		{"sanitized", map[string]string{"CUSTOM_ENV_CI_JOB_ID": "a/b c"}, "a_b_c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Key(func(k string) string { return tt.env[k] })
			if got != tt.want {
				t.Errorf("Key() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveLoadRemove(t *testing.T) {
	dir := t.TempDir()
	in := &State{MicrovmID: "mvm-1", Endpoint: "mvm-1.example.on.aws", Region: "us-east-1", Token: "secret", Port: 8080, ImageARN: "img"}

	if err := Save(dir, "job-1", in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Token is sensitive: the file must not be world-readable.
	info, err := os.Stat(Path(dir, "job-1"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state file perms = %o, want 600", perm)
	}

	out, err := Load(dir, "job-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *out != *in {
		t.Errorf("roundtrip mismatch:\n got %+v\nwant %+v", out, in)
	}

	if err := Remove(dir, "job-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := Load(dir, "job-1"); err == nil {
		t.Error("expected Load to fail after Remove")
	}
	// Removing a missing file is not an error.
	if err := Remove(dir, "job-1"); err != nil {
		t.Errorf("Remove of missing file: %v", err)
	}
}
