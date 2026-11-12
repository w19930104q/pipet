// Package e2e builds the pipet binary and drives it against a local HTTP
// server and fake commands. It verifies the whole pipeline end to end:
// HTML/JSON scraping, bad references, non-zero pipes, multi-block partial
// failures, the stable --json envelope and broken-pipe behavior.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	binaryPath string
	serverURL  string
	loopHits   int
	buildOnce  sync.Once
	buildErr   error
	tmpDir     string
)

func TestMain(m *testing.M) {
	var err error
	tmpDir, err = os.MkdirTemp("", "pipet-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	code := m.Run()
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

// setup builds the binary and starts the fixture server once per test run.
func setup(t *testing.T) {
	t.Helper()
	buildOnce.Do(func() {
		// Locate the module root from this file's location so the build does
		// not depend on the test process' working directory.
		_, thisFile, _, _ := runtime.Caller(0)
		moduleRoot := filepath.Dir(filepath.Dir(thisFile))
		binaryPath = filepath.Join(tmpDir, "pipet")
		buildCmd := exec.Command("go", "build", "-o", binaryPath,
			"github.com/bjesus/pipet/cmd/pipet")
		buildCmd.Dir = moduleRoot
		buildErr = buildCmd.Run()
	})
	require.NoError(t, buildErr, "building pipet binary")
	require.FileExists(t, binaryPath)
}

func startServer(t *testing.T) *httptest.Server {
	t.Helper()
	loopHits = 0
	mux := http.NewServeMux()

	mux.HandleFunc("/hn", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "2" {
			fmt.Fprint(w, `<html><body><h1>page-2</h1></body></html>`)
			return
		}
		fmt.Fprint(w, `<html><body>
			<a class="next" href="/hn?page=2">more</a>
			<ul><li>one</li><li>two</li></ul>
		</body></html>`)
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"item": {"name": "widget", "price": 42}}`)
	})
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	})
	mux.HandleFunc("/chain", func(w http.ResponseWriter, r *http.Request) {
		// A good first page whose next link points at the failing endpoint.
		fmt.Fprintf(w, `<html><body><h1>chain-start</h1>
			<a href="/fail">next</a></body></html>`)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		loopHits++
		fmt.Fprint(w, `<html><body><a href="/loop">again</a><h1>looping</h1></body></html>`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	serverURL = srv.URL
	return srv
}

func writeSpec(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// runPipet executes the binary and returns stdout, stderr and the exit code.
func runPipet(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return stdout.String(), stderr.String(), code
}

type jsonEnvelope struct {
	Results []struct {
		Index      int             `json:"index"`
		Pages      json.RawMessage `json:"pages"`
		StopReason string          `json:"stop_reason"`
		Error      *struct {
			Block   int    `json:"block"`
			Page    int    `json:"page"`
			Stage   string `json:"stage"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"results"`
	Errors []struct {
		Block  int    `json:"block"`
		Page   int    `json:"page"`
		Stage  string `json:"stage"`
		Reason string `json:"message"`
	} `json:"errors"`
}

// TestE2EHTMLPagination covers HTML scraping with relative pagination, in
// both text and JSON output.
func TestE2EHTMLPagination(t *testing.T) {
	setup(t)
	startServer(t)
	spec := writeSpec(t, "hn.pipet", fmt.Sprintf(`curl %s/hn
h1
> a.next
`, serverURL))

	stdout, stderr, code := runPipet(t, "--json", "--max-pages", "3", spec)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	require.Empty(t, stderr)

	var report jsonEnvelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	assert.Equal(t, "completed", r.StopReason)
	assert.Nil(t, r.Error)
	assert.Contains(t, string(r.Pages), "page-2")
	assert.Empty(t, report.Errors)

	// Text output stays compatible: page values, comma-separated.
	textOut, _, textCode := runPipet(t, spec)
	require.Equal(t, 0, textCode)
	assert.Contains(t, textOut, "page-2")
}

// TestE2EJSONScraping covers the JSON query path.
func TestE2EJSONScraping(t *testing.T) {
	setup(t)
	startServer(t)
	spec := writeSpec(t, "api.pipet", fmt.Sprintf(`curl %s/api
item.name
item.price
`, serverURL))

	stdout, stderr, code := runPipet(t, "--json", spec)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var report jsonEnvelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Results, 1)
	assert.Contains(t, string(report.Results[0].Pages), "widget")
	assert.Contains(t, string(report.Results[0].Pages), "42")
}

// TestE2EBadReference follows a next-page link that resolves to a failing
// HTTP endpoint: the first page is kept and the block stops on page 2 with a
// command error, never emitting a partial second record.
func TestE2EBadReference(t *testing.T) {
	setup(t)
	startServer(t)
	spec := writeSpec(t, "bad.pipet", fmt.Sprintf(`curl -f %s/chain
h1
> a
`, serverURL))

	stdout, stderr, code := runPipet(t, "--json", spec)
	require.Equal(t, 1, code)
	assert.Contains(t, stderr, "block 0 stopped at page 2 (command)")

	var report jsonEnvelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	assert.Equal(t, "error", r.StopReason)
	require.NotNil(t, r.Error)
	assert.Equal(t, 2, r.Error.Page)
	assert.Equal(t, "command", r.Error.Stage)
	require.Len(t, report.Errors, 1)
	// Only the completed first page is present - no partial second page.
	assert.Contains(t, string(r.Pages), "chain-start")
	assert.NotContains(t, string(r.Pages), "internal server error")
}

// TestE2ECommandFailure hits an endpoint that returns an HTTP error on the
// very first request. curl -f turns the 5xx response into a non-zero exit.
func TestE2ECommandFailure(t *testing.T) {
	setup(t)
	startServer(t)
	failSpec := writeSpec(t, "fail.pipet", fmt.Sprintf(`curl -f %s/fail
h1
`, serverURL))
	stdout, stderr, code := runPipet(t, "--json", failSpec)
	require.Equal(t, 1, code)
	assert.Contains(t, stderr, "block 0 stopped at page 1 (command)")

	var report jsonEnvelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Results, 1)
	assert.Equal(t, "error", report.Results[0].StopReason)
	require.NotNil(t, report.Results[0].Error)
	assert.Equal(t, "command", report.Results[0].Error.Stage)
	assert.Len(t, report.Errors, 1)
	// The failed page must not appear in pages.
	assert.Equal(t, "[]", string(report.Results[0].Pages))
}

// TestE2EIntervalPartialFailure makes a failing block terminate an
// --interval run immediately with a non-zero code instead of looping
// forever while hiding the failure.
func TestE2EIntervalPartialFailure(t *testing.T) {
	setup(t)
	startServer(t)
	spec := writeSpec(t, "ifail.pipet", fmt.Sprintf(`curl -f %s/fail
h1
`, serverURL))

	start := time.Now()
	_, stderr, code := runPipet(t, "--json", "--interval", "5", spec)
	elapsed := time.Since(start)

	require.Equal(t, 1, code)
	assert.Less(t, elapsed, 5*time.Second, "should not wait for the interval")
	assert.Contains(t, stderr, "(command)")
}

// TestE2EBadSelector reports an invalid CSS selector as a parse-stage error
// instead of silently producing empty values.
func TestE2EBadSelector(t *testing.T) {
	setup(t)
	startServer(t)
	spec := writeSpec(t, "selector.pipet", fmt.Sprintf(`curl %s/hn
h1 >>> [
`, serverURL))

	stdout, stderr, code := runPipet(t, "--json", spec)
	require.Equal(t, 1, code)
	assert.Contains(t, stderr, "(parse)")

	var report jsonEnvelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Results, 1)
	require.NotNil(t, report.Results[0].Error)
	assert.Equal(t, "parse", report.Results[0].Error.Stage)
	assert.Equal(t, "[]", string(report.Results[0].Pages))
}

// TestE2ENonZeroPipe verifies a failing pipe stops the block with the pipe
// stage, emits no partial record and exits non-zero.
func TestE2ENonZeroPipe(t *testing.T) {
	setup(t)
	startServer(t)
	spec := writeSpec(t, "pipe.pipet", fmt.Sprintf(`curl %s/hn
h1 | command-that-does-not-exist-12345
`, serverURL))

	stdout, stderr, code := runPipet(t, "--json", spec)
	require.Equal(t, 1, code)
	assert.Contains(t, stderr, "(pipe)")

	var report jsonEnvelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Results, 1)
	require.NotNil(t, report.Results[0].Error)
	assert.Equal(t, "pipe", report.Results[0].Error.Stage)
	assert.Equal(t, "[]", string(report.Results[0].Pages))
}

// TestE2EMultiBlockPartialFailure ensures one bad block does not prevent the
// others from running, and the good blocks' data is fully present.
func TestE2EMultiBlockPartialFailure(t *testing.T) {
	setup(t)
	startServer(t)
	spec := writeSpec(t, "multi.pipet", fmt.Sprintf(`curl %s/hn
li

curl -f %s/fail
h1

curl %s/api
item.name
`, serverURL, serverURL, serverURL))

	stdout, stderr, code := runPipet(t, "--json", spec)
	require.Equal(t, 1, code)
	assert.Contains(t, stderr, "block 1 stopped at page 1 (command)")

	var report jsonEnvelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Results, 3)

	assert.Equal(t, "completed", report.Results[0].StopReason)
	assert.Contains(t, string(report.Results[0].Pages), "one")
	assert.Equal(t, "error", report.Results[1].StopReason)
	assert.Equal(t, "[]", string(report.Results[1].Pages))
	assert.Equal(t, "completed", report.Results[2].StopReason)
	assert.Contains(t, string(report.Results[2].Pages), "widget")

	require.Len(t, report.Errors, 1)
	assert.Equal(t, 1, report.Errors[0].Block)
}

// TestE2ELoopProtection checks the self-referencing page is fetched only
// once, pagination stops with "loop" and the exit code stays zero.
func TestE2ELoopProtection(t *testing.T) {
	setup(t)
	startServer(t)

	spec := writeSpec(t, "loop.pipet", fmt.Sprintf(`curl %s/loop
h1
> a
`, serverURL))

	stdout, stderr, code := runPipet(t, "--json", "--max-pages", "20", spec)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var report jsonEnvelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Results, 1)
	assert.Equal(t, "loop", report.Results[0].StopReason)
	assert.Len(t, report.Errors, 0)
	assert.Equal(t, 1, loopHits, "looping page must be requested exactly once")
}

// TestE2EFakeCommand uses a fake curl wrapper that exits non-zero, proving
// command failures are surfaced rather than swallowed.
func TestE2EFakeCommand(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	// Pipet first looks up "curl" on PATH, so the fake must be a curl-named
	// wrapper sitting ahead of the real binary.
	fakeDir := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(fakeDir, 0o755))
	fake := filepath.Join(fakeDir, "curl")
	require.NoError(t, os.WriteFile(fake,
		[]byte("#!/bin/sh\necho 'boom' >&2\nexit 3\n"), 0o755))

	spec := writeSpec(t, "fake.pipet", `curl https://example.test
h1
`)

	cmd := exec.Command(binaryPath, "--json", spec)
	cmd.Env = append(os.Environ(), "PATH="+fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.Error(t, err)
	if exitErr, ok := err.(*exec.ExitError); ok {
		assert.Equal(t, 1, exitErr.ExitCode())
	}
	assert.Contains(t, stderr.String(), "(command)")

	var report jsonEnvelope
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.Len(t, report.Results, 1)
	assert.Equal(t, "error", report.Results[0].StopReason)
}

// TestE2EBrokenPipe closes the reader early and expects pipet to exit
// quietly with code 0 instead of being killed by SIGPIPE or reporting an
// error.
func TestE2EBrokenPipe(t *testing.T) {
	setup(t)
	startServer(t)
	spec := writeSpec(t, "bp.pipet", fmt.Sprintf(`curl %s/hn
h1
> a.next
`, serverURL))

	cmd := exec.Command(binaryPath, "--max-pages", "2", spec)
	stdoutPipe, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	// Read a single byte then close the read end, simulating `head -c1`.
	buf := make([]byte, 1)
	_, _ = io.ReadFull(stdoutPipe, buf)
	require.NoError(t, stdoutPipe.Close())

	// Drain nothing else; wait for the process to notice the broken pipe.
	err = cmd.Wait()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		require.True(t, ok)
		assert.Equal(t, 0, exitErr.ExitCode(),
			"broken pipe should end quietly, stderr: %s", string(exitErr.Stderr))
	}
}

// TestE2ETemplateOutput confirms template rendering still works with the
// historical flat data and reports a missing template as an error.
func TestE2ETemplateOutput(t *testing.T) {
	setup(t)
	startServer(t)
	dir := t.TempDir()
	spec := filepath.Join(dir, "feed.pipet")
	require.NoError(t, os.WriteFile(spec,
		[]byte(fmt.Sprintf("curl %s/api\nitem.name\n", serverURL)), 0o644))
	tpl := filepath.Join(dir, "feed.tpl")
	require.NoError(t, os.WriteFile(tpl,
		[]byte(`name={{index (index . 0) 0}}`+"\n"), 0o644))

	stdout, stderr, code := runPipet(t, spec)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.True(t, strings.Contains(stdout, "name=widget"), "got %q", stdout)

	// Explicitly passing a missing template must error, not print blanks.
	missing := filepath.Join(dir, "nope.tpl")
	_, _, code = runPipet(t, "--template", missing, spec)
	assert.NotEqual(t, 0, code)
}

// TestE2EStableJSONFieldOrder ensures results always precede errors and the
// envelope keys stay stable across repeated runs.
func TestE2EStableJSONFieldOrder(t *testing.T) {
	setup(t)
	startServer(t)
	spec := writeSpec(t, "stable.pipet", fmt.Sprintf(`curl -f %s/fail
h1
`, serverURL))

	first, _, code := runPipet(t, "--json", spec)
	require.Equal(t, 1, code)
	second, _, _ := runPipet(t, "--json", spec)
	assert.Equal(t, first, second)
	ri := strings.Index(first, `"results"`)
	ei := strings.Index(first, `"errors"`)
	assert.Greater(t, ei, ri)
	assert.Greater(t, ri, -1)
}
