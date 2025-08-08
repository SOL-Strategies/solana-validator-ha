package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHooks_Validate(t *testing.T) {
	// Test with valid hooks
	hooks := &Hooks{
		Pre: []Hook{
			{Name: "pre-hook", Command: "echo 'pre'"},
		},
		Post: []Hook{
			{Name: "post-hook", Command: "echo 'post'"},
		},
	}

	err := hooks.Validate()
	assert.NoError(t, err)

	// Test with invalid pre hook (empty name)
	hooks.Pre[0].Name = ""
	err = hooks.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hooks.pre[0]: must have a name")

	// Test with invalid pre hook (empty command)
	hooks.Pre[0].Name = "pre-hook"
	hooks.Pre[0].Command = ""
	err = hooks.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hooks.pre[0]: must have a command")

	// Test with invalid post hook (empty name)
	hooks.Pre[0].Command = "echo 'pre'"
	hooks.Post[0].Name = ""
	err = hooks.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hooks.post[0]: must have a name")
}

func TestHook_Validate(t *testing.T) {
	// Test with valid hook
	hook := &Hook{
		Name:    "test-hook",
		Command: "echo 'test'",
		Args:    []string{"arg1", "arg2"},
	}

	err := hook.Validate(true) // allow must_succeed for pre hooks
	assert.NoError(t, err)

	// Test with empty name
	hook.Name = ""
	err = hook.Validate(true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must have a name")

	// Test with empty command
	hook.Name = "test-hook"
	hook.Command = ""
	err = hook.Validate(true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must have a command")

	// Test with must_succeed on post hook (not allowed)
	hook.Command = "echo 'test'"
	hook.MustSucceed = true
	err = hook.Validate(false) // don't allow must_succeed for post hooks
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hook must_succeed not allowed for post hooks")

	// Test with must_succeed on pre hook (allowed)
	err = hook.Validate(true) // allow must_succeed for pre hooks
	assert.NoError(t, err)
}

func TestHook_Run(t *testing.T) {
	hook := &Hook{
		Name:    "test-hook",
		Command: "echo",
		Args:    []string{"hello world"},
	}

	// Test dry run
	err := hook.Run(HookRunOptions{DryRun: true})
	assert.NoError(t, err)

	// Test actual run (this will actually execute the command)
	err = hook.Run(HookRunOptions{DryRun: false})
	assert.NoError(t, err)
}

func TestHooks_RunPre(t *testing.T) {
	hooks := &Hooks{
		Pre: []Hook{
			{Name: "pre-hook-1", Command: "echo", Args: []string{"pre1"}},
			{Name: "pre-hook-2", Command: "echo", Args: []string{"pre2"}},
		},
	}

	// Test dry run
	err := hooks.RunPre(HooksRunOptions{DryRun: true})
	assert.NoError(t, err)

	// Test actual run
	err = hooks.RunPre(HooksRunOptions{DryRun: false})
	assert.NoError(t, err)
}

func TestHooks_RunPost(t *testing.T) {
	hooks := &Hooks{
		Post: []Hook{
			{Name: "post-hook-1", Command: "echo", Args: []string{"post1"}},
			{Name: "post-hook-2", Command: "echo", Args: []string{"post2"}},
		},
	}

	// Test dry run
	hooks.RunPost(HooksRunOptions{DryRun: true})

	// Test actual run
	hooks.RunPost(HooksRunOptions{DryRun: false})
}
