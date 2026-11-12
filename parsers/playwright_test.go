package parsers

import (
	"os/exec"
	"testing"

	"github.com/bjesus/pipet/common"
	"github.com/stretchr/testify/assert"
)

func TestExecutePlaywrightBlock(t *testing.T) {
	// This test drives a real headless browser and downloads Chromium on
	// first use, so it is skipped when the browser toolchain is unavailable
	// (e.g. offline CI). The curl/HTML/JSON paths cover core behavior.
	if _, err := exec.LookPath("chromium"); err != nil {
		if _, err := exec.LookPath("chromium-browser"); err != nil {
			if _, err := exec.LookPath("google-chrome"); err != nil {
				t.Skip("no headless browser found; skipping Playwright end-to-end test")
			}
		}
	}

	block := common.Block{
		Type:    "playwright",
		Command: "playwright http://example.com",
		Queries: []string{"document.querySelector(\"h1\").innerText.split(\" \")", "document.querySelector(\"h1\") | wc -c"},
	}
	result, err := ExecutePlaywrightBlock(block)
	expected := []interface{}{[]interface{}{"Example", "Domain"}, "11\n"}

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, expected, result) // Mocked JSON output

}
