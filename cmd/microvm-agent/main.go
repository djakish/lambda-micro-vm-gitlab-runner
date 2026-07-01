// Command microvm-agent runs inside a Lambda MicroVM. It accepts a shell script
// over HTTP, executes it, and streams stdout/stderr plus the final exit code
// back to the caller, and it answers the Lambda MicroVM lifecycle hooks.
//
// The /run hook is the important one — Lambda only starts forwarding endpoint
// traffic to the VM after /run returns HTTP 200, so the agent must be listening.
//
// The agent has no third-party dependencies so it snapshots cleanly and starts
// instantly from the MicroVM image.
package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/agent"
)

func main() {
	addr := envDefault("MICROVM_AGENT_ADDR", ":8080")
	workdir := envDefault("MICROVM_AGENT_WORKDIR", "/")

	srv := &http.Server{
		Addr:    addr,
		Handler: agent.NewHandler(agent.Config{Workdir: workdir}),
		// No global write timeout: /exec streams for as long as a CI job runs.
		ReadHeaderTimeout: 15 * time.Second,
	}

	log.Printf("microvm-agent listening on %s (workdir=%s)", addr, workdir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("microvm-agent: server error: %v", err)
	}
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
