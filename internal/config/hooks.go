package config

import (
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/iancoleman/strcase"
	"github.com/sol-strategies/solana-validator-ha/internal/command"
	"github.com/sol-strategies/solana-validator-ha/internal/constants"
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
	templates   *templateCache
}

// HookRunOptions represents options for running a hook
type HookRunOptions struct {
	HookType     string // "pre" or "post"
	DryRun       bool
	LoggerPrefix string
	LoggerArgs   []any
	TemplateData RoleCommandTemplateData
}

// HooksRunOptions represents options for running hooks
type HooksRunOptions struct {
	DryRun       bool
	LoggerPrefix string
	LoggerArgs   []any
	TemplateData RoleCommandTemplateData
}

func (h *Hook) PrepareTemplates(data RoleCommandTemplateData) error {
	h.templates = newTemplateCache()
	return h.templates.prepare(data, h.Command, h.Args, nil)
}

func (h *Hook) render(data RoleCommandTemplateData) (Hook, error) {
	cache := h.templates
	if cache == nil {
		cache = newTemplateCache()
	}
	rendered := *h
	var err error
	rendered.Command, err = cache.render(data, h.Command)
	if err != nil {
		return Hook{}, fmt.Errorf("failed to render hook command: %w", err)
	}
	if h.Args != nil {
		rendered.Args = make([]string, len(h.Args))
	}
	for i, arg := range h.Args {
		rendered.Args[i], err = cache.render(data, arg)
		if err != nil {
			return Hook{}, fmt.Errorf("failed to render hook args[%d]: %w", i, err)
		}
	}
	rendered.templates = nil
	return rendered, nil
}

func (h Hooks) clone() Hooks {
	cloneHooks := func(hooks []Hook) []Hook {
		if hooks == nil {
			return nil
		}
		return append([]Hook(nil), hooks...)
	}
	return Hooks{Pre: cloneHooks(h.Pre), Post: cloneHooks(h.Post)}
}

// Validate validates the hooks configuration
func (h *Hooks) Validate() error {
	// hooks.pre must all be valid if defined
	for i, hook := range h.Pre {
		if err := hook.Validate(true); err != nil {
			return fmt.Errorf("hooks.%s[%d]: %w", constants.HookTypePre, i, err)
		}
	}

	// hooks.post must all be valid if defined
	for i, hook := range h.Post {
		if err := hook.Validate(false); err != nil {
			return fmt.Errorf("hooks.%s[%d]: %w", constants.HookTypePost, i, err)
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
	rendered, err := h.render(opts.TemplateData)
	if err != nil {
		return err
	}
	loggerArgs := []any{
		"hook_name", strcase.ToSnake(h.Name),
		"command", rendered.Command,
		"args", rendered.Args,
		"dry_run", opts.DryRun,
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)

	return command.Run(command.RunOptions{
		Name:         fmt.Sprintf("%s-hook %s", opts.HookType, h.Name),
		Command:      rendered.Command,
		Args:         rendered.Args,
		DryRun:       opts.DryRun,
		LoggerPrefix: opts.LoggerPrefix,
		LoggerArgs:   loggerArgs,
		StreamOutput: true,
	})
}

// RunPre runs the pre hooks
func (h *Hooks) RunPre(opts HooksRunOptions) error {
	loggerArgs := []any{
		"hook_type", constants.HookTypePre,
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)

	// run pre hooks
	for _, hook := range h.Pre {
		err := hook.Run(HookRunOptions{
			HookType:     constants.HookTypePre,
			DryRun:       opts.DryRun,
			LoggerPrefix: opts.LoggerPrefix,
			LoggerArgs:   loggerArgs,
			TemplateData: opts.TemplateData,
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
		"hook_type", constants.HookTypePost,
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)

	// run post hooks - failures are logged but not returned
	for _, hook := range h.Post {
		err := hook.Run(HookRunOptions{
			HookType:     constants.HookTypePost,
			DryRun:       opts.DryRun,
			LoggerPrefix: opts.LoggerPrefix,
			LoggerArgs:   loggerArgs,
			TemplateData: opts.TemplateData,
		})
		if err != nil {
			log.Error("hook failed", loggerArgs...)
		}
	}
}
