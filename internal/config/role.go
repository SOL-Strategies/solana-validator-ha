package config

import (
	"fmt"

	"github.com/sol-strategies/solana-validator-ha/internal/command"
)

// RoleCommandTemplateData represents data available for command and hook templates. ConsensusMode
// is the latest detected cluster phase: unknown, tower, migrating, or alpenglow.
type RoleCommandTemplateData struct {
	ActiveIdentityKeypairFile  string
	ActiveIdentityPubkey       string
	PassiveIdentityKeypairFile string
	PassiveIdentityPubkey      string
	SelfName                   string
	ConsensusMode              string
}

// Role represents configuration for active/passive role transitions
type Role struct {
	Name      string            // Internal field - set automatically by system
	Command   string            `koanf:"command"`
	Args      []string          `koanf:"args"`
	Env       map[string]string `koanf:"env"`
	Hooks     Hooks             `koanf:"hooks"`
	templates *templateCache
}

type RoleCommandRunOptions struct {
	DryRun       bool
	LoggerPrefix string
	LoggerArgs   []any
	TemplateData RoleCommandTemplateData
}

// Validate validates the role configuration
func (r *Role) Validate() error {
	// role.command must be defined
	if r.Command == "" {
		return fmt.Errorf("role.command must be defined")
	}

	return r.Hooks.Validate()
}

// PrepareTemplates parses and validates role command and hook templates while preserving their
// source strings for execution-time rendering with the current consensus phase.
func (r *Role) PrepareTemplates(data RoleCommandTemplateData) error {
	r.templates = newTemplateCache()
	if err := r.templates.prepare(data, r.Command, r.Args, r.Env); err != nil {
		return fmt.Errorf("failed to prepare role.command, role.args, and role.env: %w", err)
	}
	for i := range r.Hooks.Pre {
		if err := r.Hooks.Pre[i].PrepareTemplates(data); err != nil {
			return fmt.Errorf("failed to prepare role.hooks.pre[%d]: %w", i, err)
		}
	}
	for i := range r.Hooks.Post {
		if err := r.Hooks.Post[i].PrepareTemplates(data); err != nil {
			return fmt.Errorf("failed to prepare role.hooks.post[%d]: %w", i, err)
		}
	}
	return nil
}

func (r *Role) render(data RoleCommandTemplateData) (*Role, error) {
	cache := r.templates
	if cache == nil {
		cache = newTemplateCache()
	}
	copy := *r
	copy.templates = nil
	var err error
	copy.Command, err = cache.render(data, r.Command)
	if err != nil {
		return nil, fmt.Errorf("failed to render command: %w", err)
	}
	if r.Args != nil {
		copy.Args = make([]string, len(r.Args))
	}
	for i, arg := range r.Args {
		copy.Args[i], err = cache.render(data, arg)
		if err != nil {
			return nil, fmt.Errorf("failed to render args[%d]: %w", i, err)
		}
	}
	if r.Env != nil {
		copy.Env = make(map[string]string, len(r.Env))
	}
	for key, value := range r.Env {
		copy.Env[key], err = cache.render(data, value)
		if err != nil {
			return nil, fmt.Errorf("failed to render env[%s]: %w", key, err)
		}
	}
	copy.Hooks = r.Hooks.clone()
	return &copy, nil
}

func (r *Role) RunCommand(opts RoleCommandRunOptions) error {
	rendered, err := r.render(opts.TemplateData)
	if err != nil {
		return fmt.Errorf("failed to render role command: %w", err)
	}
	loggerArgs := []any{
		"command", rendered.Command,
		"args", rendered.Args,
		"env", rendered.Env,
		"dry_run", opts.DryRun,
	}
	loggerArgs = append(loggerArgs, opts.LoggerArgs...)

	if opts.DryRun {
		return nil
	}

	err = command.Run(command.RunOptions{
		Name:         rendered.Name,
		Command:      rendered.Command,
		Args:         rendered.Args,
		Env:          rendered.Env,
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
