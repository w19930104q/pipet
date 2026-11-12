package parsers

import (
	"os"
	"os/exec"
	"testing"

	"github.com/bjesus/pipet/common"
	"github.com/stretchr/testify/assert"
)

// mockExecCommand replaces exec.Command in tests. The behavior is selected
// through environment variables consumed by the re-executed test binary.
func mockExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "PIPET_MOCK=" + os.Getenv("PIPET_MOCK")}
	return cmd
}

// TestHelperProcess is the re-executed mock command. It is not a real test.
func TestHelperProcess(*testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("PIPET_MOCK") {
	case "curl-fail":
		os.Stderr.WriteString("curl: (7) failed to connect")
		os.Exit(7)
	case "curl-empty":
		os.Exit(0)
	default:
		os.Stdout.Write([]byte(`{"success":true}`))
		os.Exit(0)
	}
}

func withMockedExec(t *testing.T) {
	t.Helper()
	execCommand = mockExecCommand
	t.Cleanup(func() { execCommand = exec.Command })
}

// TestExecuteCurlBlock verifies the happy path now that the mock is actually
// wired into the command execution.
func TestExecuteCurlBlock(t *testing.T) {
	withMockedExec(t)
	t.Setenv("PIPET_MOCK", "curl-ok")

	block := common.Block{
		Type:    "curl",
		Command: "curl http://example.com",
		Queries: []string{"success"},
	}
	result, _, err := ExecuteCurlBlock(block)
	assert.NoError(t, err)
	assert.Equal(t, []interface{}{true}, result)
}

// TestExecuteCurlBlockCommandFailure makes sure a non-zero curl exit stops
// the block with a "command" stage error instead of being parsed as data.
func TestExecuteCurlBlockCommandFailure(t *testing.T) {
	withMockedExec(t)
	t.Setenv("PIPET_MOCK", "curl-fail")

	block := common.Block{
		Type:    "curl",
		Command: "curl http://example.com",
		Queries: []string{"h1"},
	}
	result, _, err := ExecuteCurlBlock(block)
	assert.Error(t, err)
	assert.Nil(t, result)
	var stageErr *common.StageError
	assert.ErrorAs(t, err, &stageErr)
	assert.Equal(t, "command", stageErr.Stage)
	assert.Contains(t, err.Error(), "curl command failed")
}

// TestExecuteCurlBlockEmptyBody treats an empty response body as a command
// failure so it can never produce a page of empty values.
func TestExecuteCurlBlockEmptyBody(t *testing.T) {
	withMockedExec(t)
	t.Setenv("PIPET_MOCK", "curl-empty")

	block := common.Block{
		Type:    "curl",
		Command: "curl http://example.com",
		Queries: []string{"h1"},
	}
	result, _, err := ExecuteCurlBlock(block)
	assert.Error(t, err)
	assert.Nil(t, result)
	var stageErr *common.StageError
	assert.ErrorAs(t, err, &stageErr)
	assert.Equal(t, "command", stageErr.Stage)
	assert.Contains(t, err.Error(), "empty response body")
}

// TestExecutePipe uses the real bash to verify the success path.
func TestExecutePipe(t *testing.T) {
	output, err := ExecutePipe("pipet", "wc -c")
	assert.NoError(t, err)
	assert.Equal(t, "5\n", output)
}

// TestExecutePipeNonZero ensures non-zero pipe exits are reported with the
// "pipe" stage and stderr, not swallowed.
func TestExecutePipeNonZero(t *testing.T) {
	output, err := ExecutePipe("input", "grep nomatch-please")
	assert.Error(t, err)
	assert.Equal(t, "", output)
	var stageErr *common.StageError
	assert.ErrorAs(t, err, &stageErr)
	assert.Equal(t, "pipe", stageErr.Stage)
	assert.Contains(t, err.Error(), "failed")

	// A missing command must surface as an error too.
	_, err = ExecutePipe("input", "command-that-does-not-exist-12345")
	assert.Error(t, err)
}

func TestCalculateIndentation(t *testing.T) {
	assert.Equal(t, 4, CalculateIndentation("    indented"))
	assert.Equal(t, 0, CalculateIndentation("not indented"))
}

func TestCommandExists(t *testing.T) {
	assert.True(t, commandExists("bash"))
	assert.False(t, commandExists("fake"))
}
