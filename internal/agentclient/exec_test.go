package agentclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/agent"
)

// newTestClient wires the real agent handler behind an httptest server so the
// exec path is exercised end-to-end (client -> HTTP -> agent -> bash).
func newTestClient(t *testing.T) *Client {
	t.Helper()
	h := agent.NewHandler(agent.Config{
		Workdir: t.TempDir(),
		Logger:  log.New(io.Discard, "", 0),
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return newWithClient(ts.URL, "test-token", 8080, ts.Client())
}

func TestExecSuccess(t *testing.T) {
	c := newTestClient(t)
	var out, errb bytes.Buffer
	res := c.Exec(context.Background(), "bash", []byte("echo hello; echo oops 1>&2; exit 0"), &out, &errb)

	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if !res.Completed || res.ExitCode != 0 {
		t.Fatalf("want completed exit 0, got completed=%v code=%d", res.Completed, res.ExitCode)
	}
	if got := strings.TrimSpace(out.String()); got != "hello" {
		t.Errorf("stdout = %q, want %q", got, "hello")
	}
	if got := strings.TrimSpace(errb.String()); got != "oops" {
		t.Errorf("stderr = %q, want %q", got, "oops")
	}
}

func TestExecNonZeroExit(t *testing.T) {
	c := newTestClient(t)
	var out, errb bytes.Buffer
	res := c.Exec(context.Background(), "bash", []byte("echo working; exit 7"), &out, &errb)

	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if !res.Completed {
		t.Fatalf("expected completed run")
	}
	if res.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7", res.ExitCode)
	}
}

// TestExecLargeStreaming pushes far more output than a single frame/buffer to
// confirm the JSON Lines framing and the client's line reader handle big logs.
func TestExecLargeStreaming(t *testing.T) {
	c := newTestClient(t)
	const lines = 20000
	var out bytes.Buffer
	script := fmt.Sprintf("for i in $(seq 1 %d); do echo \"line $i\"; done", lines)
	res := c.Exec(context.Background(), "bash", []byte(script), &out, io.Discard)

	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	got := strings.Count(out.String(), "\n")
	if got != lines {
		t.Errorf("got %d lines, want %d", got, lines)
	}
	if !strings.Contains(out.String(), "line 1\n") || !strings.Contains(out.String(), fmt.Sprintf("line %d\n", lines)) {
		t.Errorf("output missing expected first/last lines")
	}
}

func TestWaitHealthy(t *testing.T) {
	c := newTestClient(t)
	if err := c.WaitHealthy(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}
}

func TestExecContextCancel(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after the long-running script starts.
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	res := c.Exec(ctx, "bash", []byte("sleep 30; echo done"), io.Discard, io.Discard)
	if res.Completed && res.ExitCode == 0 {
		t.Fatalf("expected cancellation to interrupt the run, got clean exit 0")
	}
}
