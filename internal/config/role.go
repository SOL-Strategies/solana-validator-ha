package config

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/sol-strategies/solana-validator-ha/internal/command"
)

// RoleCommandTemplateData represents data available for command templates
type RoleCommandTemplateData struct {
	ActiveIdentityKeypairFile  string
	ActiveIdentityPubkey       string
	PassiveIdentityKeypairFile string
	PassiveIdentityPubkey      string
	SelfName                   string
}

// Role represents configuration for active/passive role transitions
type Role struct {
	Command string   `koanf:"command"`
	Args    []string `koanf:"args"`
	Hooks   Hooks    `koanf:"hooks"`
}

type RoleCommandRunOptions struct {
	DryRun     bool
	LoggerArgs []any
}

// Validate validates the role configuration
func (r *Role) Validate() error {
	// role.command must be defined
	if r.Command == "" {
		return fmt.Errorf("role.command must be defined")
	}

	return r.Hooks.Validate()
}

// RenderCommands renders the role commands
func (r *Role) RenderCommands(data RoleCommandTemplateData) (err error) {
	// render role.command and role.args
	r.Command, r.Args, err = r.renderCommandAndArgs(data, r.Command, r.Args)
	if err != nil {
		return fmt.Errorf("failed to render role.command and role.args: %w", err)
	}

	// render role.hooks.pre
	for i, hook := range r.Hooks.Pre {
		r.Hooks.Pre[i].Command, r.Hooks.Pre[i].Args, err = r.renderCommandAndArgs(data, hook.Command, hook.Args)
		if err != nil {
			return fmt.Errorf("failed to render role.hooks.pre[%d]: %w", i, err)
		}
	}

	// render role.hooks.post
	for i, hook := range r.Hooks.Post {
		r.Hooks.Post[i].Command, r.Hooks.Post[i].Args, err = r.renderCommandAndArgs(data, hook.Command, hook.Args)
		if err != nil {
			return fmt.Errorf("failed to render role.hooks.post[%d]: %w", i, err)
		}
	}

	return nil
}

func (r *Role) renderCommandAndArgs(data RoleCommandTemplateData, command string, args []string) (renderedCommand string, renderedArgs []string, err error) {
	// render command
	renderedCommand, err = r.renderTemplateString(data, command)
	if err != nil {
		return "", nil, fmt.Errorf("failed to render command: %w", err)
	}

	// render args
	for i, arg := range args {
		args[i], err = r.renderTemplateString(data, arg)
		if err != nil {
			return "", nil, fmt.Errorf("failed to render args[%d]: %w", i, err)
		}
	}

	return renderedCommand, args, nil
}

func (r *Role) renderTemplateString(data RoleCommandTemplateData, templateStr string) (rendered string, err error) {
	// Parse and execute template
	tmpl, err := template.New("command").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse command template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute command template: %w", err)
	}

	return buf.String(), nil
}

func (r *Role) RunCommand(opts RoleCommandRunOptions) error {
	loggerArgs := []any{
		"command", r.Command,
		"args", r.Args,
		"dry_run", opts.DryRun,
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)

	if opts.DryRun {
		return nil
	}

	err := command.Run(command.RunOptions{
		Command:    r.Command,
		Args:       r.Args,
		DryRun:     opts.DryRun,
		LoggerArgs: loggerArgs,
	})
	if err != nil {
		return fmt.Errorf("failed to run command: %w", err)
	}

	return nil
}
