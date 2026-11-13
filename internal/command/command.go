package command

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/logging"
)

var (
	stderrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("124"))
	stdoutStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("28"))
)

// RunOptions are the options for running a command
type RunOptions struct {
	Name         string
	Command      string
	Args         []string
	Env          map[string]string
	DryRun       bool
	StreamOutput bool
	LoggerPrefix string
	LoggerArgs   []any
}

// Run runs a command with the given options.
// Note: This function never times out - commands can take an indeterminate amount of time
// (e.g., failover commands that may need to wait for services to start/stop).
func Run(opts RunOptions) error {
	logger := logging.New(opts.LoggerPrefix, fmt.Sprintf("command %s", opts.Name))
	envString := ""
	for key, value := range opts.Env {
		envString += fmt.Sprintf("%s=%s ", key, value)
	}
	runMsg := fmt.Sprintf("%s %s %s", envString, opts.Command, strings.Join(opts.Args, " "))
	runMsg = strings.TrimSpace(runMsg)

	logger.Info(runMsg, "dry_run", opts.DryRun)

	// if dry run, skip command execution
	if opts.DryRun {
		logger.Debug("command execution skipped - dry run")
		return nil
	}

	// execute command for realsies
	cmd := exec.Command(opts.Command, opts.Args...)

	// Set environment variables if provided
	if len(opts.Env) > 0 {
		cmd.Env = make([]string, 0, len(opts.Env))
		for key, value := range opts.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", strings.TrimSpace(key), strings.TrimSpace(value)))
		}
	}

	if opts.StreamOutput {
		return runWithStreaming(cmd, logger)
	}

	return runWithoutStreaming(cmd, logger)
}

// runWithStreaming drains output before returning and retains bounded failure diagnostics.
func runWithStreaming(cmd *exec.Cmd, logger *log.Logger) error {
	stdout := &streamWriter{logger: logger, stream: "stdout"}
	stderr := &streamWriter{logger: logger, stream: "stderr"}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// This bounds output draining after exit, not command execution.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	stdout.flush()
	stderr.flush()
	if errors.Is(err, exec.ErrWaitDelay) {
		logger.Warn("command exited successfully but output draining timed out; output may be truncated")
		err = nil
	}
	if err != nil {
		if tail := strings.TrimSpace(string(stderr.tail)); tail != "" {
			err = fmt.Errorf("%w; stderr tail: %s", err, tail)
		}
		logger.Error("failed to run command", "error", err)
		return err
	}
	logger.Debug("command completed successfully")
	return nil
}

const stderrTailLimit = 4096

// streamWriter logs lines or bounded chunks. Each writer is owned by one os/exec
// copy goroutine until Run returns, after which the caller flushes it.
type streamWriter struct {
	logger  *log.Logger
	stream  string
	pending []byte
	tail    []byte
}

func (w *streamWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.stream == "stderr" {
		tail := p
		if len(tail) >= stderrTailLimit {
			w.tail = w.tail[:0]
			tail = tail[len(tail)-stderrTailLimit:]
		} else if excess := len(w.tail) + len(tail) - stderrTailLimit; excess > 0 {
			copy(w.tail, w.tail[excess:])
			w.tail = w.tail[:len(w.tail)-excess]
		}
		w.tail = append(w.tail, tail...)
	}
	for len(p) > 0 {
		size := min(len(p), stderrTailLimit-len(w.pending))
		if newline := bytes.IndexByte(p[:size], '\n'); newline >= 0 {
			size = newline + 1
		}
		w.pending = append(w.pending, p[:size]...)
		p = p[size:]
		if len(w.pending) == stderrTailLimit || w.pending[len(w.pending)-1] == '\n' {
			w.flush()
		}
	}
	return n, nil
}

func (w *streamWriter) flush() {
	if len(w.pending) == 0 {
		return
	}
	w.logger.Info(styledStreamOutputString(w.stream, strings.TrimRight(string(w.pending), "\r\n")))
	w.pending = w.pending[:0]
}

// runWithoutStreaming executes the command and captures all output (original behavior)
func runWithoutStreaming(cmd *exec.Cmd, logger *log.Logger) error {
	// Capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Error("failed to create stdout pipe", "error", err)
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		logger.Error("failed to create stderr pipe", "error", err)
		return err
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		logger.Error("failed to start command", "error", err)
		return err
	}

	// Read stdout and stderr
	stdoutBytes, err := io.ReadAll(stdout)
	if err != nil {
		logger.Error("failed to read stdout", "error", err)
		return err
	}

	stderrBytes, err := io.ReadAll(stderr)
	if err != nil {
		logger.Error("failed to read stderr", "error", err)
		return err
	}

	// Wait for command to complete
	err = cmd.Wait()
	if err != nil {
		logger.Error("failed to run command",
			"error", err,
			"stdout", string(stdoutBytes),
			"stderr", string(stderrBytes),
		)
		return err
	}

	logger.Debug("command completed successfully")
	return nil
}

// styledStreamOutputString creates a styled string for stream output
func styledStreamOutputString(stream string, text string) string {
	streamStyle := stdoutStyle
	if stream == "stderr" {
		streamStyle = stderrStyle
	}
	return fmt.Sprintf("%s %s", streamStyle.Render(">"), text)
}
