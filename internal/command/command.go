package command

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
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
	LoggerArgs   []any
}

// Run runs a command with the given options.
// Note: This function never times out - commands can take an indeterminate amount of time
// (e.g., failover commands that may need to wait for services to start/stop).
func Run(opts RunOptions) error {
	logger := log.WithPrefix(fmt.Sprintf("command[%s]", opts.Name))

	logger.With(
		"cmd", opts.Command,
		"args", opts.Args,
		"env", opts.Env,
		"dry_run", opts.DryRun,
		"stream_output", opts.StreamOutput,
	).Info("running")

	if opts.DryRun {
		logger.Debug("command completed successfully")
		return nil
	}

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

// runWithStreaming executes the command and streams stdout/stderr in real-time
func runWithStreaming(cmd *exec.Cmd, logger *log.Logger) error {
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

	// Stream stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			logger.Info(styledStreamOutputString("stdout", scanner.Text()))
		}
	}()

	// Stream stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logger.Info(styledStreamOutputString("stderr", scanner.Text()))
		}
	}()

	// Wait for command to complete
	err = cmd.Wait()
	if err != nil {
		logger.Error("failed to run command", "error", err)
		return err
	}

	logger.Debug("command completed successfully")
	return nil
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
