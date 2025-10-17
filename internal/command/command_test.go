package command

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_Success(t *testing.T) {
	// Create a simple test script that always succeeds
	scriptPath := createTestScript(t, "echo 'hello world'", 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "success",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed")
}

func TestRun_DryRun(t *testing.T) {
	opts := RunOptions{
		Command: "nonexistent-command",
		Args:    []string{"arg1", "arg2"},
		DryRun:  true,
		LoggerArgs: []any{
			"test", "dry_run",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected dry run to succeed")
}

func TestRun_Failure(t *testing.T) {
	// Create a test script that always fails
	scriptPath := createTestScript(t, "exit 1", 1)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "failure",
		},
	}

	err := Run(opts)
	assert.Error(t, err, "expected command to fail")
}

func TestRun_WithOutput(t *testing.T) {
	// Create a test script that outputs to stdout
	scriptPath := createTestScript(t, "echo 'test output'", 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "output",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed")
}

func TestRun_WithStderr(t *testing.T) {
	// Create a test script that outputs to stderr
	scriptPath := createTestScript(t, "echo 'error message' >&2", 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "stderr",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed")
}

func TestRun_InvalidCommand(t *testing.T) {
	opts := RunOptions{
		Command: "nonexistent-command-that-should-fail",
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "invalid_command",
		},
	}

	err := Run(opts)
	assert.Error(t, err, "expected command to fail")
}

func TestRun_CommandWithLongOutput(t *testing.T) {
	// Create a test script that generates long output
	scriptContent := `for i in {1..100}; do echo "line $i"; done`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "long_output",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed")
}

func TestRun_CommandWithSpecialCharacters(t *testing.T) {
	// Create a test script that handles special characters
	scriptContent := `echo "test with spaces and \"quotes\" and 'single quotes'"`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "special_chars",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed")
}

func TestRun_EmptyArgs(t *testing.T) {
	// Create a test script that works with no args
	scriptPath := createTestScript(t, "echo 'no args'", 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "empty_args",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed")
}

func TestRun_WithArgs(t *testing.T) {
	// Create a test script that uses arguments
	scriptContent := `echo "arg1: $1, arg2: $2"`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{"value1", "value2"},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "with_args",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed")
}

func TestRun_WithLoggerArgs(t *testing.T) {
	scriptPath := createTestScript(t, "echo 'test'", 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"custom_field", "custom_value",
			"another_field", 123,
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed")
}

func TestRun_CommandWithExitCodes(t *testing.T) {
	// Test various exit codes
	exitCodes := []int{0, 1, 2, 127}

	for _, exitCode := range exitCodes {
		t.Run(fmt.Sprintf("ExitCode_%d", exitCode), func(t *testing.T) {
			scriptPath := createTestScript(t, fmt.Sprintf("exit %d", exitCode), exitCode)
			defer os.Remove(scriptPath)

			opts := RunOptions{
				Command: scriptPath,
				Args:    []string{},
				DryRun:  false,
				LoggerArgs: []any{
					"test", "exit_code",
					"expected_code", exitCode,
				},
			}

			err := Run(opts)
			if exitCode == 0 {
				assert.NoError(t, err, "expected command to succeed with exit code 0")
			} else {
				assert.Error(t, err, "expected command to fail with non-zero exit code")
			}
		})
	}
}

func TestRun_CommandWithLargeOutput(t *testing.T) {
	// Create a test script that generates large output
	scriptContent := `for i in {1..1000}; do echo "This is line number $i with some additional text to make it longer"; done`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "large_output",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed with large output")
}

func TestRun_CommandWithUnicode(t *testing.T) {
	// Create a test script that handles unicode characters
	scriptContent := `echo "Hello 世界! 🌍"`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "unicode",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed with unicode")
}

func TestRun_CommandWithNewlines(t *testing.T) {
	// Create a test script that outputs multiple lines
	scriptContent := `echo "line 1"; echo "line 2"; echo "line 3"`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "newlines",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed with newlines")
}

func TestRun_CommandWithMixedOutput(t *testing.T) {
	// Create a test script that outputs to both stdout and stderr
	scriptContent := `echo "stdout message"; echo "stderr message" >&2; echo "more stdout"`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "mixed_output",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed with mixed output")
}

func TestRun_CommandWithEnvironment(t *testing.T) {
	// Create a test script that uses environment variables
	scriptContent := `echo "TEST_VAR: $TEST_VAR"`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "environment",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed with environment")
}

func TestRun_CommandWithWorkingDirectory(t *testing.T) {
	// Create a test script that shows working directory
	scriptContent := `pwd`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "working_directory",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed with working directory")
}

func TestRun_InvalidExecutable(t *testing.T) {
	opts := RunOptions{
		Command: "/nonexistent/path/to/executable",
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "invalid_executable",
		},
	}

	err := Run(opts)
	assert.Error(t, err, "expected command to fail with invalid executable")
}

func TestRun_CommandNotFound(t *testing.T) {
	opts := RunOptions{
		Command: "command_that_does_not_exist_12345",
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "command_not_found",
		},
	}

	err := Run(opts)
	assert.Error(t, err, "expected command to fail when command not found")
}

func TestRun_CommandWithTimeout(t *testing.T) {
	// Create a test script that sleeps for a short time
	scriptContent := `sleep 1`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "timeout",
		},
	}

	// Run in a goroutine with a reasonable timeout for testing
	done := make(chan error, 1)
	go func() {
		done <- Run(opts)
	}()

	select {
	case err := <-done:
		assert.NoError(t, err, "expected command to succeed")
	case <-time.After(5 * time.Second):
		t.Error("command took too long in test environment")
	}
}

func TestRun_CommandWithComplexArgs(t *testing.T) {
	// Create a test script that handles complex arguments
	scriptContent := `echo "arg1: '$1'"; echo "arg2: '$2'"; echo "arg3: '$3'"`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	complexArgs := []string{
		"simple",
		"with spaces",
		"with\"quotes\"",
	}

	opts := RunOptions{
		Command: scriptPath,
		Args:    complexArgs,
		DryRun:  false,
		LoggerArgs: []any{
			"test", "complex_args",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed with complex arguments")
}

func TestRun_CommandWithEmptyStringArgs(t *testing.T) {
	// Create a test script that handles empty string arguments
	scriptContent := `echo "arg1: '$1'"; echo "arg2: '$2'"`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{"", "non-empty"},
		DryRun:  false,
		LoggerArgs: []any{
			"test", "empty_string_args",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command to succeed with empty string arguments")
}

func TestRun_WithStreaming(t *testing.T) {
	// Create a test script that outputs to both stdout and stderr
	scriptContent := `#!/bin/sh
echo "This is stdout line 1"
echo "This is stderr line 1" >&2
echo "This is stdout line 2"
echo "This is stderr line 2" >&2
echo "This is stdout line 3"
`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command:      scriptPath,
		Args:         []string{},
		DryRun:       false,
		StreamOutput: true,
		LoggerArgs: []any{
			"test", "streaming",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected streaming command to succeed")
}

func TestRun_WithStreamingAndFailure(t *testing.T) {
	// Create a test script that outputs to both stdout and stderr then fails
	scriptContent := `#!/bin/sh
echo "This is stdout before failure"
echo "This is stderr before failure" >&2
exit 1
`
	scriptPath := createTestScript(t, scriptContent, 1)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command:      scriptPath,
		Args:         []string{},
		DryRun:       false,
		StreamOutput: true,
		LoggerArgs: []any{
			"test", "streaming_failure",
		},
	}

	err := Run(opts)
	assert.Error(t, err, "expected streaming command to fail")
}

// Benchmark tests
func BenchmarkRun_Success(b *testing.B) {
	scriptPath := createTestScript(b, "echo 'benchmark test'", 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command: scriptPath,
		Args:    []string{},
		DryRun:  false,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Run(opts)
	}
}

func BenchmarkRun_DryRun(b *testing.B) {
	opts := RunOptions{
		Command: "nonexistent-command",
		Args:    []string{},
		DryRun:  true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Run(opts)
	}
}

// Helper functions
func createTestScript(t testing.TB, content string, expectedExitCode int) string {
	// Create a temporary script file
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "test_script.sh")

	// Write the script content
	err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"+content), 0755)
	require.NoError(t, err, "failed to create test script")

	return scriptPath
}

func TestRun_WithEnvironmentVariables(t *testing.T) {
	// Create a test script that outputs environment variables
	scriptContent := `#!/bin/sh
echo "TEST_VAR1: $TEST_VAR1"
echo "TEST_VAR2: $TEST_VAR2"
echo "PATH: $PATH"
`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	env := map[string]string{
		"TEST_VAR1": "value1",
		"TEST_VAR2": "value2",
	}

	opts := RunOptions{
		Command:      scriptPath,
		Args:         []string{},
		Env:          env,
		DryRun:       false,
		StreamOutput: false,
		LoggerArgs: []any{
			"test", "env_vars",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command with env vars to succeed")
}

func TestRun_WithEnvironmentVariablesStreaming(t *testing.T) {
	// Create a test script that outputs environment variables
	scriptContent := `#!/bin/sh
echo "TEST_VAR1: $TEST_VAR1"
echo "TEST_VAR2: $TEST_VAR2"
echo "PATH: $PATH"
`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	env := map[string]string{
		"TEST_VAR1": "value1",
		"TEST_VAR2": "value2",
	}

	opts := RunOptions{
		Command:      scriptPath,
		Args:         []string{},
		Env:          env,
		DryRun:       false,
		StreamOutput: true,
		LoggerArgs: []any{
			"test", "env_vars_streaming",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected streaming command with env vars to succeed")
}

func TestRun_WithEmptyEnvironmentVariables(t *testing.T) {
	// Create a test script that outputs environment variables
	scriptContent := `#!/bin/sh
echo "TEST_VAR1: $TEST_VAR1"
echo "TEST_VAR2: $TEST_VAR2"
echo "PATH: $PATH"
`
	scriptPath := createTestScript(t, scriptContent, 0)
	defer os.Remove(scriptPath)

	opts := RunOptions{
		Command:      scriptPath,
		Args:         []string{},
		Env:          map[string]string{}, // Empty env map
		DryRun:       false,
		StreamOutput: false,
		LoggerArgs: []any{
			"test", "empty_env_vars",
		},
	}

	err := Run(opts)
	assert.NoError(t, err, "expected command with empty env vars to succeed")
}
