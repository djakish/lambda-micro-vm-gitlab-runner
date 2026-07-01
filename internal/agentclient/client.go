// Package agentclient talks to the in-VM exec agent over the MicroVM's HTTPS
// endpoint, authenticating with the JWE token minted by create-microvm-auth-token.
package agentclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/djakish/lambda-micro-vm-gitlab-runner/internal/protocol"
)

// Headers understood by the Lambda MicroVM ingress proxy.
const (
	headerAuth = "X-aws-proxy-auth"
	headerPort = "X-aws-proxy-port"
)

// Client targets one MicroVM endpoint.
type Client struct {
	base  string // scheme://host, no trailing slash
	token string
	port  int
	http  *http.Client
}

// New builds a client for a MicroVM endpoint/token/port.
func New(endpoint, token string, port int) *Client {
	return newWithClient("https://"+endpoint, token, port, &http.Client{
		// No client-wide timeout: /exec streams for the life of a CI stage.
		// Connection establishment is bounded by the dialer below.
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	})
}

// newWithClient is the shared constructor; tests use it to point at a local
// plain-HTTP server.
func newWithClient(base, token string, port int, hc *http.Client) *Client {
	return &Client{base: base, token: token, port: port, http: hc}
}

func (c *Client) url(path string) string {
	return c.base + path
}

func (c *Client) auth(req *http.Request) {
	req.Header.Set(headerAuth, c.token)
	req.Header.Set(headerPort, strconv.Itoa(c.port))
}

// WaitHealthy polls the agent /healthz until it answers 200 or the timeout
// elapses. Early attempts are expected to fail while the VM finishes booting.
func (c *Client) WaitHealthy(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := c.ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("agent did not become healthy within %s: %w", timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Client) ping(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, c.url(protocol.HealthPath), nil)
	if err != nil {
		return err
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz status %d", resp.StatusCode)
	}
	return nil
}

// ExecResult reports how a script run finished.
type ExecResult struct {
	// ExitCode is the script's shell exit status. Valid only when Completed.
	ExitCode int
	// Completed is true when the agent reported an exit frame. When false the
	// run failed at the transport/agent level (a system failure, not a job
	// failure) and Err explains why.
	Completed bool
	Err       error
}

// Exec streams a script to the agent and copies its output to stdout/stderr as
// it arrives. It returns once the agent reports the process exit code or the
// stream fails.
func (c *Client) Exec(ctx context.Context, shell string, script []byte, stdout, stderr io.Writer) ExecResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(protocol.ExecPath), bytes.NewReader(script))
	if err != nil {
		return ExecResult{Err: err}
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/x-sh")
	if shell != "" {
		req.Header.Set(protocol.HeaderShell, shell)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return ExecResult{Err: fmt.Errorf("exec request failed: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return ExecResult{Err: fmt.Errorf("exec endpoint returned %d: %s", resp.StatusCode, body)}
	}

	return consume(resp.Body, stdout, stderr)
}

// consume parses the JSON Lines stream and dispatches frames.
func consume(body io.Reader, stdout, stderr io.Writer) ExecResult {
	br := bufio.NewReaderSize(body, 128<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if res, terminal := dispatch(line, stdout, stderr); terminal {
				return res
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ExecResult{Err: errors.New("exec stream ended without an exit frame")}
			}
			return ExecResult{Err: fmt.Errorf("read exec stream: %w", err)}
		}
	}
}

// dispatch handles one frame. It returns terminal=true once the run is decided.
func dispatch(line []byte, stdout, stderr io.Writer) (ExecResult, bool) {
	var fr protocol.Frame
	if err := json.Unmarshal(line, &fr); err != nil {
		// Ignore blank lines / partial noise; only fail on a decode of real data.
		if len(trimSpace(line)) == 0 {
			return ExecResult{}, false
		}
		return ExecResult{Err: fmt.Errorf("decode frame: %w", err)}, true
	}
	switch fr.Kind {
	case protocol.KindStdout:
		writeDecoded(stdout, fr.Data)
	case protocol.KindStderr:
		writeDecoded(stderr, fr.Data)
	case protocol.KindExit:
		return ExecResult{ExitCode: fr.Code, Completed: true}, true
	case protocol.KindError:
		return ExecResult{Err: fmt.Errorf("agent error: %s", fr.Message)}, true
	}
	return ExecResult{}, false
}

func writeDecoded(w io.Writer, data string) {
	if data == "" {
		return
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return
	}
	_, _ = w.Write(b)
}

func trimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\r' || b[j-1] == '\n') {
		j--
	}
	return b[i:j]
}
