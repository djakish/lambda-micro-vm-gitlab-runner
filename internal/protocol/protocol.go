// Package protocol defines the wire format spoken between the driver CLI
// (running on the GitLab Runner host) and the exec agent (running inside the
// Lambda MicroVM).
//
// Lambda MicroVMs expose no SSH or native exec API — the only way in is the
// per-VM HTTPS endpoint. So the agent implements a tiny "run this script and
// stream me the output" protocol over HTTP.
//
// The driver POSTs a shell script to the agent's /exec endpoint. The agent
// replies with a chunked stream of newline-delimited JSON objects (JSON Lines).
// Each line is one Frame. Output is base64-encoded so a chunk can never contain
// a newline that would corrupt the framing, and so binary output survives the
// round trip. The stream is terminated by exactly one FrameExit (clean run) or
// one FrameError (the agent could not run the script at all).
//
// JSON Lines is deliberately chosen over HTTP trailers: it survives any proxy
// that preserves ordinary chunked/streamed HTTP bodies (which the MicroVM
// ingress does, since it supports SSE and gRPC), and it conveys the exit code
// inline without a second request.
package protocol

const (
	// ExecPath is the agent route that runs a script.
	ExecPath = "/exec"
	// HealthPath is the agent readiness probe used by the driver's prepare stage.
	HealthPath = "/healthz"

	// HeaderShell selects the interpreter used to run the posted script.
	// Value is one of ShellBash / ShellSh. Defaults to bash when absent.
	HeaderShell = "X-Microvm-Shell"
	// HeaderExecID carries a caller-supplied correlation id, echoed into agent logs.
	HeaderExecID = "X-Microvm-Exec-Id"

	ShellBash = "bash"
	ShellSh   = "sh"
)

// Frame kinds.
const (
	KindStdout = "stdout" // process wrote to stdout; Data is base64
	KindStderr = "stderr" // process wrote to stderr; Data is base64
	KindExit   = "exit"   // process finished; Code is its exit status (terminal)
	KindError  = "error"  // agent-side failure before/around exec; Message set (terminal)
)

// Frame is a single JSON Lines record in the /exec response stream.
type Frame struct {
	Kind string `json:"kind"`
	// Data holds base64-encoded output bytes for stdout/stderr frames.
	Data string `json:"data,omitempty"`
	// Code is the process exit status for an exit frame.
	Code int `json:"code,omitempty"`
	// Message describes an agent-side error for an error frame.
	Message string `json:"message,omitempty"`
}
