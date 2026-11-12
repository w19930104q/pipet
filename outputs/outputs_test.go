package outputs

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/bjesus/pipet/common"
	"github.com/stretchr/testify/assert"
)

func sampleApp() *common.PipetApp {
	return &common.PipetApp{
		Data: []interface{}{
			[]interface{}{"a", "b"},
			[]interface{}{"c"},
		},
		Results: []common.BlockResult{
			{
				Index:      0,
				Type:       "curl",
				Pages:      []interface{}{[]interface{}{"a", "b"}},
				StopReason: "completed",
			},
			{
				Index:      1,
				Type:       "curl",
				Pages:      []interface{}{},
				StopReason: "error",
				Error: &common.BlockError{
					Block:   1,
					Page:    2,
					Stage:   "pipe",
					Message: "pipe \"bad\" failed: exit status 1",
				},
			},
		},
	}
}

// TestOutputJSONEnvelope keeps the stable results/errors contract and makes
// sure no partial record leaks into the result list.
func TestOutputJSONEnvelope(t *testing.T) {
	out, err := OutputJSON(sampleApp())
	assert.NoError(t, err)

	var report JSONReport
	assert.NoError(t, json.Unmarshal([]byte(out), &report))

	assert.Len(t, report.Results, 2)
	assert.Equal(t, "completed", report.Results[0].StopReason)
	assert.Equal(t, "error", report.Results[1].StopReason)
	assert.Empty(t, report.Results[1].Pages)

	if assert.Len(t, report.Errors, 1) {
		assert.Equal(t, 1, report.Errors[0].Block)
		assert.Equal(t, 2, report.Errors[0].Page)
		assert.Equal(t, "pipe", report.Errors[0].Stage)
	}

	// Field order must be stable across runs.
	assert.True(t, strings.Contains(out, "\"results\""))
	firstResults := strings.Index(out, "\"results\"")
	firstErrors := strings.Index(out, "\"errors\"")
	assert.Less(t, firstResults, firstErrors)
}

// TestOutputTextCompatibility verifies the text output still renders from
// the flat Data slice exactly as before the refactor.
func TestOutputTextCompatibility(t *testing.T) {
	app := sampleApp()
	app.Separator = []string{"|"}
	out := OutputText(app)
	// Top-level pages join with "|"; elements within a page use the default
	// ", " separator.
	assert.Equal(t, "a, b|c", out)

	// No custom separators: everything uses the historical default.
	app.Separator = nil
	assert.Equal(t, "a, b, c", OutputText(app))
}

// errWriter always returns EPIPE, mimicking a pipe whose reader went away.
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) {
	return 0, syscall.EPIPE
}

// TestWriteBrokenPipe normalizes EPIPE reported by the writer to
// ErrBrokenPipe.
func TestWriteBrokenPipe(t *testing.T) {
	err := Write(errWriter{}, "more data")
	assert.True(t, errors.Is(err, ErrBrokenPipe), "expected ErrBrokenPipe, got %v", err)
}

// TestWriteClosedPipe normalizes the "file already closed" error produced by
// writing to a closed pipe.
func TestWriteClosedPipe(t *testing.T) {
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	assert.NoError(t, r.Close())
	assert.NoError(t, w.Close())

	err = Write(w, "more data")
	assert.True(t, errors.Is(err, ErrBrokenPipe), "expected ErrBrokenPipe, got %v", err)
}

// TestWriteRealBrokenPipe reproduces an actual broken pipe through os.Pipe.
func TestWriteRealBrokenPipe(t *testing.T) {
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	assert.NoError(t, r.Close())

	// The first write may succeed into the pipe buffer; keep writing until
	// the kernel reports EPIPE.
	gotEPIPE := false
	for i := 0; i < 1000; i++ {
		if err := Write(w, strings.Repeat("x", 65536)); err != nil {
			if errors.Is(err, ErrBrokenPipe) {
				gotEPIPE = true
			}
			break
		}
	}
	_ = w.Close()
	assert.True(t, gotEPIPE, "expected an EPIPE from a real broken pipe")
}

// TestWriteSuccess covers the normal path through the shared writer.
func TestWriteSuccess(t *testing.T) {
	var buf bytes.Buffer
	assert.NoError(t, Write(&buf, "hello"))
	assert.Equal(t, "hello", buf.String())
}

// TestOutputTemplateError reports a missing template instead of silently
// producing empty output.
func TestOutputTemplateError(t *testing.T) {
	app := sampleApp()
	_, err := OutputTemplate(app, filepath.Join(t.TempDir(), "missing.tpl"))
	assert.Error(t, err)
}

// TestOutputTemplateHappyPath keeps template rendering working with the
// historical flat Data structure.
func TestOutputTemplateHappyPath(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "ok.tpl")
	assert.NoError(t, os.WriteFile(tpl,
		[]byte(`{{range .}}{{index . 0}};{{end}}`), 0o644))

	out, err := OutputTemplate(sampleApp(), tpl)
	assert.NoError(t, err)
	assert.Equal(t, "a;c;", out)
}
