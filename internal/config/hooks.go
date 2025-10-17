package config

import (
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/iancoleman/strcase"
	"github.com/sol-strategies/solana-validator-ha/internal/command"
)

// Hooks represents a pre/post hook command
type Hooks struct {
	Pre  []Hook `koanf:"pre"`
	Post []Hook `koanf:"post"`
}

// Hook represents a pre/post hook command
type Hook struct {
	Name        string   `koanf:"name"`
	Command     string   `koanf:"command"`
	Args        []string `koanf:"args"`
	MustSucceed bool     `koanf:"must_succeed"`
}

// HookRunOptions represents options for running a hook
type HookRunOptions struct {
	HookType   string // "pre" or "post"
	DryRun     bool
	LoggerArgs []any
}

// HooksRunOptions represents options for running hooks
type HooksRunOptions struct {
	DryRun     bool
	LoggerArgs []any
}

// Validate validates the hooks configuration
func (h *Hooks) Validate() error {
	// hooks.pre must all be valid if defined
	for i, hook := range h.Pre {
		if err := hook.Validate(true); err != nil {
			return fmt.Errorf("hooks.pre[%d]: %w", i, err)
		}
	}

	// hooks.post must all be valid if defined
	for i, hook := range h.Post {
		if err := hook.Validate(false); err != nil {
			return fmt.Errorf("hooks.post[%d]: %w", i, err)
		}
	}

	return nil
}

// Validate validates the hook configuration
func (h *Hook) Validate(allowMustSucceed bool) error {
	// hook.name must be defined
	if h.Name == "" {
		return fmt.Errorf("must have a name")
	}

	// hook.command must be defined
	if h.Command == "" {
		return fmt.Errorf("must have a command")
	}

	if !allowMustSucceed && h.MustSucceed {
		return fmt.Errorf("hook must_succeed not allowed for post hooks")
	}

	return nil
}

func (h *Hook) Run(opts HookRunOptions) error {
	loggerArgs := []any{
		"hook_name", strcase.ToSnake(h.Name),
		"command", h.Command,
		"args", h.Args,
		"dry_run", opts.DryRun,
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)

	if opts.DryRun {
		return nil
	}

	log.Info("running hook", loggerArgs...)
	return command.Run(command.RunOptions{
		Name:         fmt.Sprintf("%s-hook %s", opts.HookType, h.Name),
		Command:      h.Command,
		Args:         h.Args,
		DryRun:       opts.DryRun,
		LoggerArgs:   loggerArgs,
		StreamOutput: true,
	})
}

// RunPre runs the pre hooks
func (h *Hooks) RunPre(opts HooksRunOptions) error {
	loggerArgs := []any{
		"hook_type", "pre",
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)

	// run pre hooks
	for _, hook := range h.Pre {
		err := hook.Run(HookRunOptions{
			HookType:   "pre",
			DryRun:     opts.DryRun,
			LoggerArgs: loggerArgs,
		})
		if err != nil && hook.MustSucceed {
			return err
		}
		if err != nil && !hook.MustSucceed {
			log.Error("hook failed", loggerArgs...)
		}
	}

	return nil
}

// RunPost runs the post hooks
func (h *Hooks) RunPost(opts HooksRunOptions) {
	loggerArgs := []any{
		"hook_type", "post",
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)

	// run post hooks - failures are logged but not returned
	for _, hook := range h.Post {
		err := hook.Run(HookRunOptions{
			HookType:   "post",
			DryRun:     opts.DryRun,
			LoggerArgs: loggerArgs,
		})
		if err != nil {
			log.Error("hook failed", loggerArgs...)
		}
	}
}
