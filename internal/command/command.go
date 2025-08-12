package command

import (
	"io"
	"os/exec"

	"github.com/charmbracelet/log"
)

// RunOptions are the options for running a command
type RunOptions struct {
	Command    string
	Args       []string
	DryRun     bool
	LoggerArgs []any
}

// Run runs a command with the given options.
// Note: This function never times out - commands can take an indeterminate amount of time
// (e.g., failover commands that may need to wait for services to start/stop).
func Run(opts RunOptions) error {
	logger := log.WithPrefix("command_runner")
	loggerArgs := []any{
		"command", opts.Command,
		"args", opts.Args,
		"dry_run", opts.DryRun,
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)
	logger.Info("running command", loggerArgs...)

	if opts.DryRun {
		logger.Debug("command completed successfully", loggerArgs...)
		return nil
	}

	cmd := exec.Command(opts.Command, opts.Args...)

	// Capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		loggerArgs = append(loggerArgs, "error", err)
		logger.Error("failed to create stdout pipe", loggerArgs...)
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		loggerArgs = append(loggerArgs, "error", err)
		logger.Error("failed to create stderr pipe", loggerArgs...)
		return err
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		loggerArgs = append(loggerArgs, "error", err)
		logger.Error("failed to start command", loggerArgs...)
		return err
	}

	// Read stdout and stderr
	stdoutBytes, err := io.ReadAll(stdout)
	if err != nil {
		loggerArgs = append(loggerArgs, "error", err)
		logger.Error("failed to read stdout", loggerArgs...)
		return err
	}

	stderrBytes, err := io.ReadAll(stderr)
	if err != nil {
		loggerArgs = append(loggerArgs, "error", err)
		logger.Error("failed to read stderr", loggerArgs...)
		return err
	}

	// Wait for command to complete
	err = cmd.Wait()
	if err != nil {
		loggerArgs = append(loggerArgs,
			"error", err,
			"stdout", string(stdoutBytes),
			"stderr", string(stderrBytes),
		)
		logger.Error("failed to run command", loggerArgs...)
		return err
	}

	logger.Debug("command completed successfully", loggerArgs...)

	return nil
}
