package command

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/require"
)

func TestStreamingFailureRetainsBoundedStderrTail(t *testing.T) {
	const reason = "cannot positively fence peer; staying passive"
	output := strings.Repeat("x", 256*1024)
	script := createTestScript(t, "printf '%s' 'stdout-"+output+"' &\nprintf '%s' 'discarded-prefix-"+output+reason+"' >&2\nwait\nexit 7", 7)
	defer os.Remove(script)
	err := Run(RunOptions{Command: script, StreamOutput: true})
	require.ErrorContains(t, err, reason)
	require.NotContains(t, err.Error(), "discarded-prefix")
	require.NotContains(t, err.Error(), "stdout-")
	require.LessOrEqual(t, len(err.Error()), stderrTailLimit+64)
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 7, exitErr.ExitCode())
}

func TestStreamingFlushesFinalOutput(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output)
	cmd := exec.Command("/bin/sh", "-c", "printf 'first\\nlast stdout'; printf 'last stderr' >&2")
	require.NoError(t, runWithStreaming(cmd, logger))
	for _, expected := range []string{"first", "last stdout", "last stderr"} {
		require.Contains(t, output.String(), expected)
	}
}

func TestStreamingDoesNotWaitForBackgroundDescendant(t *testing.T) {
	for _, exitCode := range []int{0, 7} {
		t.Run(strconv.Itoa(exitCode), func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			t.Cleanup(func() {
				data, err := os.ReadFile(pidFile)
				if err != nil {
					return
				}
				pid, err := strconv.Atoi(string(data))
				if err != nil {
					return
				}
				process, err := os.FindProcess(pid)
				if err == nil {
					_ = process.Kill()
				}
			})
			var output bytes.Buffer
			cmd := exec.Command("/bin/sh", "-c", `sleep 30 & printf '%s' "$!" > "$1"; printf 'final diagnostic' >&2; exit "$2"`, "test", pidFile, strconv.Itoa(exitCode))
			start := time.Now()
			err := runWithStreaming(cmd, log.New(&output))
			require.Less(t, time.Since(start), 5*time.Second)
			require.Contains(t, output.String(), "final diagnostic")
			if exitCode == 0 {
				require.NoError(t, err)
				require.Contains(t, output.String(), "output may be truncated")
			} else {
				require.ErrorContains(t, err, "final diagnostic")
				var exitErr *exec.ExitError
				require.True(t, errors.As(err, &exitErr))
				require.Equal(t, exitCode, exitErr.ExitCode())
			}
		})
	}
}

func TestStreamWriterChunksAndRetainsTailAcrossWrites(t *testing.T) {
	var output bytes.Buffer
	w := &streamWriter{logger: log.New(&output), stream: "stderr"}
	for _, chunk := range []string{strings.Repeat("a", 4000), strings.Repeat("b", 2000), "\nfinal"} {
		n, err := w.Write([]byte(chunk))
		require.NoError(t, err)
		require.Equal(t, len(chunk), n)
		require.Less(t, len(w.pending), stderrTailLimit)
		require.LessOrEqual(t, len(w.tail), stderrTailLimit)
	}
	w.flush()
	require.Equal(t, strings.Repeat("a", 2090)+strings.Repeat("b", 2000)+"\nfinal", string(w.tail))
	require.Contains(t, output.String(), "final")
}
