package config

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/sol-strategies/solana-validator-ha/internal/command"
)

// RoleCommandTemplateData represents data available for command templates
type RoleCommandTemplateData struct {
	ProfileName                string
	ProfilePriority            int
	VoteAccountPubkey          string
	AuthorizedVoterPubkey      string
	ActiveIdentityKeypairFile  string
	ActiveIdentityPubkey       string
	PassiveIdentityKeypairFile string
	PassiveIdentityPubkey      string
	SelfName                   string
}

// Role represents configuration for active/passive role transitions
type Role struct {
	Name    string            // Internal field - set automatically by system
	Command string            `koanf:"command"`
	Args    []string          `koanf:"args"`
	Env     map[string]string `koanf:"env"`
	Hooks   Hooks             `koanf:"hooks"`
}

type RoleCommandRunOptions struct {
	DryRun       bool
	LoggerPrefix string
	LoggerArgs   []any
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
	// render role.command, role.args, and role.env
	err = r.renderCommandAndArgs(data)
	if err != nil {
		return fmt.Errorf("failed to render role.command, role.args, and role.env: %w", err)
	}

	// render role.hooks.pre
	for i := range r.Hooks.Pre {
		err = r.renderHook(data, &r.Hooks.Pre[i])
		if err != nil {
			return fmt.Errorf("failed to render role.hooks.pre[%d]: %w", i, err)
		}
	}

	// render role.hooks.post
	for i := range r.Hooks.Post {
		err = r.renderHook(data, &r.Hooks.Post[i])
		if err != nil {
			return fmt.Errorf("failed to render role.hooks.post[%d]: %w", i, err)
		}
	}

	return nil
}

// RenderedCopy returns a rendered copy of the role without mutating the source config.
func (r Role) RenderedCopy(data RoleCommandTemplateData) (Role, error) {
	rendered := Role{
		Name:    r.Name,
		Command: r.Command,
		Args:    append([]string(nil), r.Args...),
		Hooks: Hooks{
			Pre:  copyHooks(r.Hooks.Pre),
			Post: copyHooks(r.Hooks.Post),
		},
	}
	if r.Env != nil {
		rendered.Env = make(map[string]string, len(r.Env))
		for k, v := range r.Env {
			rendered.Env[k] = v
		}
	}
	if err := rendered.RenderCommands(data); err != nil {
		return Role{}, err
	}
	return rendered, nil
}

func copyHooks(hooks []Hook) []Hook {
	copied := append([]Hook(nil), hooks...)
	for i := range copied {
		copied[i].Args = append([]string(nil), copied[i].Args...)
	}
	return copied
}

func (r *Role) renderCommandAndArgs(data RoleCommandTemplateData) (err error) {
	// render command
	r.Command, err = r.renderTemplateString(data, r.Command)
	if err != nil {
		return fmt.Errorf("failed to render command: %w", err)
	}

	// render args
	for i, arg := range r.Args {
		r.Args[i], err = r.renderTemplateString(data, arg)
		if err != nil {
			return fmt.Errorf("failed to render args[%d]: %w", i, err)
		}
	}

	// render environment variables
	for key, value := range r.Env {
		r.Env[key], err = r.renderTemplateString(data, value)
		if err != nil {
			return fmt.Errorf("failed to render env[%s]: %w", key, err)
		}
	}

	return nil
}

func (r *Role) renderHook(data RoleCommandTemplateData, hook *Hook) (err error) {
	// render hook command
	hook.Command, err = r.renderTemplateString(data, hook.Command)
	if err != nil {
		return fmt.Errorf("failed to render hook command: %w", err)
	}

	// render hook args
	for i, arg := range hook.Args {
		hook.Args[i], err = r.renderTemplateString(data, arg)
		if err != nil {
			return fmt.Errorf("failed to render hook args[%d]: %w", i, err)
		}
	}

	return nil
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
		"env", r.Env,
		"dry_run", opts.DryRun,
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)

	if opts.DryRun {
		return nil
	}

	err := command.Run(command.RunOptions{
		Name:         r.Name,
		Command:      r.Command,
		Args:         r.Args,
		Env:          r.Env,
		DryRun:       opts.DryRun,
		LoggerPrefix: opts.LoggerPrefix,
		LoggerArgs:   loggerArgs,
		StreamOutput: true,
	})
	if err != nil {
		return fmt.Errorf("failed to run command: %w", err)
	}

	return nil
}
