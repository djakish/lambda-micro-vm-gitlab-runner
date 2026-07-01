// Command microvm-executor is the GitLab Runner custom-executor driver for AWS
// Lambda MicroVMs. GitLab invokes it once per stage:
//
//	microvm-executor config     -> prints builds_dir/cache_dir/shell as JSON
//	microvm-executor prepare    -> launches a MicroVM and waits for its agent
//	microvm-executor run <file> <substage> -> runs a script inside the MicroVM
//	microvm-executor cleanup    -> terminates the MicroVM
//
// Each stage is a separate process; prepare hands the MicroVM id/endpoint/token
// to run and cleanup through a per-job state file. See the internal/state and
// internal/config packages for details.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/agentclient"
	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/config"
	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/microvm"
	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/protocol"
	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/setup"
	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/state"
)

func main() {
	if len(os.Args) < 2 {
		systemFailuref("usage: microvm-executor <setup|config|prepare|run|cleanup|version> [args]")
	}
	switch os.Args[1] {
	// setup and version are human-facing; the rest are GitLab-invoked stages.
	case "version", "--version", "-v":
		fmt.Println(version)
	case "setup":
		cmdSetup(os.Args[2:])
	case "config":
		cmdConfig()
	case "prepare":
		cmdPrepare()
	case "run":
		cmdRun(os.Args[2:])
	case "cleanup":
		cmdCleanup()
	default:
		systemFailuref("unknown stage %q", os.Args[1])
	}
}

// cmdSetup runs the interactive installer. Unlike the job stages, it uses plain
// 0/1 exit codes since it is run by a human, not GitLab.
func cmdSetup(args []string) {
	opts := setup.Options{}
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			opts.AssumeYes = true
		case "--no-color":
			opts.NoColor = true
		case "-h", "--help":
			fmt.Println("Usage: microvm-executor setup [--yes] [--no-color]")
			fmt.Println("Interactive installer: provisions AWS prerequisites, publishes the")
			fmt.Println("MicroVM image, and writes a GitLab Runner config.toml.")
			return
		default:
			fmt.Fprintf(os.Stderr, "setup: unknown flag %q (try --help)\n", a)
			os.Exit(2)
		}
	}
	if err := setup.Run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "\nsetup failed: %v\n", err)
		os.Exit(1)
	}
}

// cmdConfig prints the executor configuration GitLab reads before every job.
func cmdConfig() {
	cfg, err := config.Load()
	if err != nil {
		systemFailure(err)
	}
	out := map[string]any{
		"builds_dir":           cfg.BuildsDir,
		"cache_dir":            cfg.CacheDir,
		"builds_dir_is_shared": false, // each job gets its own isolated MicroVM
		"shell":                protocol.ShellBash,
		"driver": map[string]string{
			"name":    "AWS Lambda MicroVMs",
			"version": version,
		},
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		systemFailure(err)
	}
}

// cmdPrepare launches the MicroVM for this job and waits until its agent is
// reachable. State is saved as soon as the VM exists so cleanup can always find
// and terminate it, even if a later step here fails.
func cmdPrepare() {
	cfg, err := config.Load()
	if err != nil {
		systemFailure(err)
	}
	ctx, stop := signalContext()
	defer stop()

	cli := microvm.New(cfg.AWSCLI, cfg.Region)
	key := state.Key(os.Getenv)

	// Reap any MicroVM left over from a previous prepare attempt (system-failure
	// retries re-run prepare without an intervening cleanup).
	reapPrevious(ctx, cli, cfg.StateDir, key)

	logf("launching MicroVM from image %s in %s", cfg.ImageARN, cfg.Region)
	run, err := cli.RunMicrovm(ctx, microvm.RunInput{
		ImageIdentifier:    cfg.ImageARN,
		IngressConnectors:  cfg.IngressConnectors,
		EgressConnectors:   cfg.EgressConnectors,
		ExecutionRoleARN:   cfg.ExecutionRoleARN,
		IdlePolicyJSON:     cfg.IdlePolicyJSON,
		MaxDurationSeconds: cfg.MaxDurationSeconds,
	})
	if err != nil {
		systemFailure(err)
	}

	st := &state.State{
		MicrovmID: run.MicrovmID,
		Endpoint:  run.Endpoint,
		Region:    cfg.Region,
		Port:      cfg.AgentPort,
		ImageARN:  cfg.ImageARN,
	}
	if err := state.Save(cfg.StateDir, key, st); err != nil {
		// Terminate the just-created VM: without saved state cleanup can't reap it.
		_ = cli.Terminate(ctx, run.MicrovmID)
		systemFailure(fmt.Errorf("save state: %w", err))
	}
	logf("MicroVM %s created, endpoint %s", run.MicrovmID, run.Endpoint)

	if err := cli.WaitRunning(ctx, run.MicrovmID, dur(cfg.RunTimeoutSeconds)); err != nil {
		systemFailure(err)
	}
	logf("MicroVM %s is RUNNING", run.MicrovmID)

	token, err := cli.CreateAuthToken(ctx, run.MicrovmID, cfg.TokenTTLMinutes, cfg.AgentPort)
	if err != nil {
		systemFailure(err)
	}
	st.Token = token
	if err := state.Save(cfg.StateDir, key, st); err != nil {
		systemFailure(fmt.Errorf("save state: %w", err))
	}

	ac := agentclient.New(run.Endpoint, token, cfg.AgentPort)
	if err := ac.WaitHealthy(ctx, dur(cfg.HealthTimeoutSeconds)); err != nil {
		systemFailure(err)
	}
	logf("agent healthy; MicroVM ready for job")
}

// cmdRun streams one job sub-stage script into the MicroVM and maps the result
// onto GitLab's build/system failure contract.
func cmdRun(args []string) {
	if len(args) < 2 {
		systemFailuref("run requires <script-path> <sub-stage>, got %v", args)
	}
	scriptPath, subStage := args[0], args[1]

	cfg, err := config.Load()
	if err != nil {
		systemFailure(err)
	}
	st, err := state.Load(cfg.StateDir, state.Key(os.Getenv))
	if err != nil {
		systemFailure(fmt.Errorf("load state for run stage %q: %w", subStage, err))
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		systemFailure(fmt.Errorf("read script %s: %w", scriptPath, err))
	}

	ctx, stop := signalContext()
	defer stop()

	ac := agentclient.New(st.Endpoint, st.Token, st.Port)
	res := ac.Exec(ctx, protocol.ShellBash, script, os.Stdout, os.Stderr)
	if res.Err != nil {
		// Transport/agent-level problem: a system failure, so GitLab may retry.
		systemFailure(fmt.Errorf("run stage %q: %w", subStage, res.Err))
	}
	if res.ExitCode != 0 {
		buildFailure(res.ExitCode)
	}
}

// cmdCleanup terminates the MicroVM. Per the custom-executor contract, cleanup
// failures do not fail the job, so problems are logged but the process exits 0.
func cmdCleanup() {
	cfg, err := config.Load()
	if err != nil {
		warnf("cleanup: %v", err)
		os.Exit(0)
	}
	key := state.Key(os.Getenv)
	st, err := state.Load(cfg.StateDir, key)
	if err != nil {
		// No state => nothing was created (or it was already reaped). Nothing to do.
		os.Exit(0)
	}

	ctx, stop := signalContext()
	defer stop()

	cli := microvm.New(cfg.AWSCLI, cfg.Region)
	if err := cli.Terminate(ctx, st.MicrovmID); err != nil {
		warnf("cleanup: terminate MicroVM %s failed (it may need manual removal): %v", st.MicrovmID, err)
	} else {
		logf("terminated MicroVM %s", st.MicrovmID)
	}
	if err := state.Remove(cfg.StateDir, key); err != nil {
		warnf("cleanup: remove state: %v", err)
	}
	os.Exit(0)
}

// reapPrevious terminates and clears a MicroVM recorded by an earlier prepare
// attempt for the same job, so retries don't leak VMs.
func reapPrevious(ctx context.Context, cli *microvm.Client, dir, key string) {
	st, err := state.Load(dir, key)
	if err != nil {
		return
	}
	logf("reaping MicroVM %s from a previous prepare attempt", st.MicrovmID)
	if err := cli.Terminate(ctx, st.MicrovmID); err != nil {
		warnf("could not terminate stale MicroVM %s: %v", st.MicrovmID, err)
	}
	_ = state.Remove(dir, key)
}

// --- GitLab failure contract helpers -------------------------------------

// buildFailure reports a failed job script: it records the real exit code for
// allow_failure handling, then exits with BUILD_FAILURE_EXIT_CODE.
func buildFailure(code int) {
	if f := os.Getenv("BUILD_EXIT_CODE_FILE"); f != "" {
		_ = os.WriteFile(f, []byte(strconv.Itoa(code)), 0o644)
	}
	os.Exit(envExitCode("BUILD_FAILURE_EXIT_CODE"))
}

// systemFailure reports a transient/infra problem so GitLab can retry the stage.
func systemFailure(err error) {
	fmt.Fprintf(os.Stderr, "microvm-executor: %v\n", err)
	os.Exit(envExitCode("SYSTEM_FAILURE_EXIT_CODE"))
}

func systemFailuref(format string, a ...any) { systemFailure(fmt.Errorf(format, a...)) }

// envExitCode reads a GitLab-provided exit code, defaulting to 1 when unset (as
// happens during local testing outside a real job).
func envExitCode(name string) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 1
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

func dur(seconds int) time.Duration { return time.Duration(seconds) * time.Second }

func logf(format string, a ...any) { fmt.Fprintf(os.Stderr, "microvm-executor: "+format+"\n", a...) }
func warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "microvm-executor: WARN "+format+"\n", a...)
}

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"
