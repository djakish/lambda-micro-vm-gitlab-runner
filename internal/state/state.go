// Package state persists per-job MicroVM details across the custom-executor
// stages. config, prepare, run and cleanup each run as a *separate* process, so
// prepare must hand the MicroVM id / endpoint / auth token to the later stages
// through a file keyed by the GitLab job.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// State is the handoff record written by prepare and read by run/cleanup.
type State struct {
	MicrovmID string `json:"microvm_id"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Token     string `json:"token"`
	Port      int    `json:"port"`
	ImageARN  string `json:"image_arn"`
}

// Key derives a stable, filesystem-safe identifier for a job from its GitLab
// environment. CI_JOB_ID is unique per GitLab instance; project id is included
// for readability when inspecting the state directory.
func Key(env func(string) string) string {
	job := env("CUSTOM_ENV_CI_JOB_ID")
	proj := env("CUSTOM_ENV_CI_PROJECT_ID")
	switch {
	case proj != "" && job != "":
		return sanitize(proj + "-" + job)
	case job != "":
		return sanitize(job)
	default:
		// Fall back to the concurrent slot so two jobs never collide even if the
		// (unexpected) job id is missing.
		return sanitize("job-" + env("CUSTOM_ENV_CI_CONCURRENT_ID"))
	}
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "job"
	}
	return string(out)
}

// Path returns the state file location for a job key inside dir.
func Path(dir, key string) string {
	return filepath.Join(dir, key+".json")
}

// Save writes the state atomically (write temp + rename) with 0600 perms; the
// token it holds is sensitive.
func Save(dir, key string, s *State) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	final := Path(dir, key)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("commit state: %w", err)
	}
	return nil
}

// Load reads the state file for a job key.
func Load(dir, key string) (*State, error) {
	b, err := os.ReadFile(Path(dir, key))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", Path(dir, key), err)
	}
	return &s, nil
}

// Remove deletes the state file; a missing file is not an error.
func Remove(dir, key string) error {
	err := os.Remove(Path(dir, key))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
