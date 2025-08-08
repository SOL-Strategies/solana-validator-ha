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

func TestRole_RenderCommands(t *testing.T) {
	role := &Role{
		Command: "systemctl {{.ActiveIdentityPubkey}}",
		Args:    []string{"--identity", "{{.ActiveIdentityKeypairFile}}"},
		Hooks: Hooks{
			Pre: []Hook{
				{Name: "pre-hook", Command: "echo '{{.PassiveIdentityPubkey}}'"},
			},
			Post: []Hook{
				{Name: "post-hook", Command: "echo '{{.PassiveIdentityKeypairFile}}'"},
			},
		},
	}

	data := RoleCommandTemplateData{
		ActiveIdentityKeypairFile:  "/path/to/active.json",
		ActiveIdentityPubkey:       "active-pubkey",
		PassiveIdentityKeypairFile: "/path/to/passive.json",
		PassiveIdentityPubkey:      "passive-pubkey",
	}

	err := role.RenderCommands(data)
	assert.NoError(t, err)

	// Check that templates were rendered
	assert.Equal(t, "systemctl active-pubkey", role.Command)
	assert.Equal(t, []string{"--identity", "/path/to/active.json"}, role.Args)
	assert.Equal(t, "echo 'passive-pubkey'", role.Hooks.Pre[0].Command)
	assert.Equal(t, "echo '/path/to/passive.json'", role.Hooks.Post[0].Command)
}

func TestRole_RenderCommandsWithInvalidTemplate(t *testing.T) {
	role := &Role{
		Command: "systemctl {{.InvalidField}}",
	}

	data := RoleCommandTemplateData{
		ActiveIdentityKeypairFile: "/path/to/active.json",
		ActiveIdentityPubkey:      "active-pubkey",
	}

	err := role.RenderCommands(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to render role.command and role.args")
}

func TestRole_RenderTemplateString(t *testing.T) {
	role := &Role{}
	data := RoleCommandTemplateData{
		ActiveIdentityPubkey: "test-pubkey",
	}

	// Test simple template
	result, err := role.renderTemplateString(data, "echo {{.ActiveIdentityPubkey}}")
	assert.NoError(t, err)
	assert.Equal(t, "echo test-pubkey", result)

	// Test template with multiple fields
	result, err = role.renderTemplateString(data, "{{.ActiveIdentityPubkey}} {{.PassiveIdentityPubkey}}")
	assert.NoError(t, err)
	assert.Equal(t, "test-pubkey ", result) // PassiveIdentityPubkey is empty

	// Test invalid template
	_, err = role.renderTemplateString(data, "{{.InvalidField}}")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute command template")
}
