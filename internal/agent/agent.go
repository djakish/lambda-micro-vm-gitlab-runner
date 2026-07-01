// Package agent implements the in-VM HTTP service that runs job scripts and
// answers Lambda MicroVM lifecycle hooks. It lives in its own package (rather
// than in cmd/microvm-agent) so the exec path can be tested end-to-end against
// the real handler.
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/protocol"
)

// hookBase is the path prefix Lambda uses for MicroVM lifecycle hooks.
const hookBase = "/aws/lambda-microvms/runtime/v1/"

// Config configures the agent handler.
type Config struct {
	// Workdir is the directory scripts run in. GitLab's generated scripts cd to
	// the build dir themselves, so this is only the starting point.
	Workdir string
	// Logger receives operational messages. Defaults to the standard logger.
	Logger *log.Logger
}

func (c Config) logger() *log.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return log.Default()
}

// NewHandler builds the agent's HTTP handler.
func NewHandler(cfg Config) http.Handler {
	if cfg.Workdir == "" {
		cfg.Workdir = "/"
	}
	logger := cfg.logger()

	mux := http.NewServeMux()

	mux.HandleFunc(protocol.HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})

	mux.HandleFunc(protocol.ExecPath, func(w http.ResponseWriter, r *http.Request) {
		handleExec(w, r, cfg.Workdir, logger)
	})

	// All lifecycle hooks just acknowledge with 200. /run in particular must
	// succeed for Lambda to start forwarding endpoint traffic to the VM.
	for _, name := range []string{"run", "ready", "validate", "resume", "suspend", "terminate"} {
		mux.HandleFunc(hookBase+name, hookHandler(name, logger))
	}
	return mux
}

func hookHandler(name string, logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10)); len(body) > 0 {
			logger.Printf("lifecycle hook %q: %s", name, body)
		} else {
			logger.Printf("lifecycle hook %q", name)
		}
		w.WriteHeader(http.StatusOK)
	}
}

func handleExec(w http.ResponseWriter, r *http.Request, workdir string, logger *log.Logger) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	script, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	shell := r.Header.Get(protocol.HeaderShell)
	if shell != protocol.ShellSh {
		shell = protocol.ShellBash
	}
	execID := r.Header.Get(protocol.HeaderExecID)
	logger.Printf("exec start id=%q shell=%s bytes=%d", execID, shell, len(script))

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	fw := &frameWriter{w: w, f: flusher}

	// Persist the script and run it. `-e` is intentionally not forced: GitLab's
	// generated scripts manage their own error handling.
	f, err := os.CreateTemp("", "microvm-exec-*.sh")
	if err != nil {
		fw.send(protocol.Frame{Kind: protocol.KindError, Message: "create temp script: " + err.Error()})
		return
	}
	scriptPath := f.Name()
	defer os.Remove(scriptPath)
	if _, err := f.Write(script); err != nil {
		_ = f.Close()
		fw.send(protocol.Frame{Kind: protocol.KindError, Message: "write temp script: " + err.Error()})
		return
	}
	_ = f.Close()

	cmd := exec.Command(shell, scriptPath)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fw.send(protocol.Frame{Kind: protocol.KindError, Message: "stdout pipe: " + err.Error()})
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fw.send(protocol.Frame{Kind: protocol.KindError, Message: "stderr pipe: " + err.Error()})
		return
	}
	if err := cmd.Start(); err != nil {
		fw.send(protocol.Frame{Kind: protocol.KindError, Message: "start process: " + err.Error()})
		return
	}

	// Kill the whole process group if the caller disconnects (job canceled).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		<-ctx.Done()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go pump(&wg, fw, protocol.KindStdout, stdout)
	go pump(&wg, fw, protocol.KindStderr, stderr)
	wg.Wait()

	code := exitCode(cmd.Wait())
	fw.send(protocol.Frame{Kind: protocol.KindExit, Code: &code})
	logger.Printf("exec done id=%q code=%d", execID, code)
}

func pump(wg *sync.WaitGroup, fw *frameWriter, kind string, r io.Reader) {
	defer wg.Done()
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			fw.send(protocol.Frame{Kind: kind, Data: base64.StdEncoding.EncodeToString(buf[:n])})
		}
		if err != nil {
			return
		}
	}
}

// exitCode maps cmd.Wait()'s result to a conventional shell exit status.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if c := ee.ExitCode(); c >= 0 {
			return c
		}
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return 1
	}
	return 1
}

// frameWriter serializes and flushes JSON Lines records so output streams live.
type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
	f  http.Flusher
}

func (fw *frameWriter) send(fr protocol.Frame) {
	b, err := json.Marshal(fr)
	if err != nil {
		return
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()
	_, _ = fw.w.Write(append(b, '\n'))
	fw.f.Flush()
}
