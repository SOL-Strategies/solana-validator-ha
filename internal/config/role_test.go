package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRole_Validate(t *testing.T) {
	// Test with valid role
	role := &Role{
		Command: "systemctl start solana",
		Args:    []string{"--identity", "/path/to/identity.json"},
		Hooks: Hooks{
			Pre: []Hook{
				{Name: "pre-hook", Command: "echo 'pre'"},
			},
			Post: []Hook{
				{Name: "post-hook", Command: "echo 'post'"},
			},
		},
	}

	err := role.Validate()
	assert.NoError(t, err)

	// Test with empty command
	role.Command = ""
	err = role.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "role.command must be defined")
}

func TestRole_PrepareAndRenderTemplates(t *testing.T) {
	role := &Role{
		Command: "systemctl {{.ActiveIdentityPubkey}} {{.ConsensusMode}}",
		Args:    []string{"--identity", "{{.ActiveIdentityKeypairFile}}", "--phase", "{{.ConsensusMode}}"},
		Env: map[string]string{
			"SOLANA_IDENTITY": "{{.ActiveIdentityPubkey}}",
			"SOLANA_KEYPAIR":  "{{.ActiveIdentityKeypairFile}}",
			"SOLANA_SELF":     "{{.SelfName}}",
			"SOLANA_PHASE":    "{{.ConsensusMode}}",
		},
		Hooks: Hooks{
			Pre: []Hook{
				{Name: "pre-hook", Command: "echo '{{.PassiveIdentityPubkey}} {{.ConsensusMode}}'", Args: []string{"{{.ConsensusMode}}"}},
			},
			Post: []Hook{
				{Name: "post-hook", Command: "echo '{{.PassiveIdentityKeypairFile}} {{.ConsensusMode}}'"},
			},
		},
	}

	data := RoleCommandTemplateData{
		ActiveIdentityKeypairFile:  "/path/to/active.json",
		ActiveIdentityPubkey:       "active-pubkey",
		PassiveIdentityKeypairFile: "/path/to/passive.json",
		PassiveIdentityPubkey:      "passive-pubkey",
		SelfName:                   "validator-1",
	}

	err := role.PrepareTemplates(data)
	assert.NoError(t, err)

	// Startup validation retains templates so the phase can be supplied later.
	assert.Equal(t, "systemctl {{.ActiveIdentityPubkey}} {{.ConsensusMode}}", role.Command)
	assert.Equal(t, "echo '{{.PassiveIdentityPubkey}} {{.ConsensusMode}}'", role.Hooks.Pre[0].Command)

	for _, phase := range []string{"unknown", "tower", "migrating", "alpenglow"} {
		renderData := data
		renderData.ConsensusMode = phase
		rendered, err := role.render(renderData)
		assert.NoError(t, err)
		assert.Equal(t, "systemctl active-pubkey "+phase, rendered.Command)
		assert.Equal(t, []string{"--identity", "/path/to/active.json", "--phase", phase}, rendered.Args)
		assert.Equal(t, phase, rendered.Env["SOLANA_PHASE"])

		preHook, err := role.Hooks.Pre[0].render(renderData)
		assert.NoError(t, err)
		assert.Equal(t, "echo 'passive-pubkey "+phase+"'", preHook.Command)
		assert.Equal(t, []string{phase}, preHook.Args)
		postHook, err := role.Hooks.Post[0].render(renderData)
		assert.NoError(t, err)
		assert.Equal(t, "echo '/path/to/passive.json "+phase+"'", postHook.Command)
	}

	assert.Equal(t, "{{.ConsensusMode}}", role.Env["SOLANA_PHASE"])
}

func TestRole_PrepareTemplatesRejectsInvalidTemplate(t *testing.T) {
	role := &Role{
		Command: "systemctl {{.InvalidField}}",
	}

	data := RoleCommandTemplateData{
		ActiveIdentityKeypairFile: "/path/to/active.json",
		ActiveIdentityPubkey:      "active-pubkey",
	}

	err := role.PrepareTemplates(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to prepare role.command, role.args, and role.env")
}

func TestRole_PrepareTemplatesRejectsInvalidEnvTemplate(t *testing.T) {
	role := &Role{
		Command: "systemctl start solana",
		Env: map[string]string{
			"SOLANA_IDENTITY": "{{.InvalidField}}",
		},
	}

	data := RoleCommandTemplateData{
		ActiveIdentityKeypairFile: "/path/to/active.json",
		ActiveIdentityPubkey:      "active-pubkey",
	}

	err := role.PrepareTemplates(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute env[SOLANA_IDENTITY] template")
}

func TestHookRenderUsesInvocationPhase(t *testing.T) {
	hook := &Hook{Name: "phase", Command: "notify", Args: []string{"{{.ConsensusMode}}"}}
	assert.NoError(t, hook.PrepareTemplates(RoleCommandTemplateData{ConsensusMode: "unknown"}))

	rendered, err := hook.render(RoleCommandTemplateData{ConsensusMode: "alpenglow"})
	assert.NoError(t, err)
	assert.Equal(t, []string{"alpenglow"}, rendered.Args)
	assert.Equal(t, []string{"{{.ConsensusMode}}"}, hook.Args)
}
