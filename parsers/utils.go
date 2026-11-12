package parsers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/bjesus/pipet/common"
	"github.com/google/shlex"
)

// execCommand is indirected so tests can replace it with a mock.
var execCommand = exec.Command

func ExecuteCurlBlock(block common.Block) (interface{}, string, error) {
	if !commandExists("curl") {
		return nil, "", common.NewStageError("command",
			fmt.Errorf("curl command not found. Please install curl and try again"))
	}

	var parts []string
	switch cmd := block.Command.(type) {
	case string:
		parts, _ = shlex.Split(cmd)
	case []string:
		parts = cmd
	default:
	}

	if len(parts) == 0 {
		return nil, "", common.NewStageError("command", fmt.Errorf("empty curl command"))
	}

	cmd := execCommand(parts[0], parts[1:]...)
	output, err := cmd.Output()
	if err != nil {
		return nil, "", common.NewStageError("command",
			fmt.Errorf("curl command failed: %w\nOutput: %s", err, string(output)))
	}

	if len(bytes.TrimSpace(output)) == 0 {
		return nil, "", common.NewStageError("command",
			fmt.Errorf("curl command returned an empty response body"))
	}

	isJSON := json.Valid(bytes.TrimSpace(output))

	if isJSON {
		data, _, err := ParseJSONQueries(output, block.Queries)
		if err != nil {
			return nil, "", err
		}
		return data, "", nil
	}

	data, nextPageURL, err := ParseHTMLQueries(output, block.Queries, block.NextPage)
	if err != nil {
		return nil, "", err
	}
	return data, nextPageURL, nil
}

// ExecutePipe runs command through bash feeding it input on stdin. A non-zero
// exit is reported as an error (including the command's stderr) instead of
// being silently dropped, so callers can stop the current block.
func ExecutePipe(input string, command string) (string, error) {
	cmd := execCommand("bash", "-c", command)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		if stderr != "" {
			return "", common.NewStageError("pipe",
				fmt.Errorf("pipe %q failed: %w: %s", command, err, stderr))
		}
		return "", common.NewStageError("pipe",
			fmt.Errorf("pipe %q failed: %w", command, err))
	}
	return string(output), nil
}

func CalculateIndentation(s string) int {
	count := 0
	for _, char := range s {
		if char != ' ' {
			break
		}
		count++
	}
	return count
}

func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}
