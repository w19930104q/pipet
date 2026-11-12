package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bjesus/pipet/common"
	"github.com/stretchr/testify/assert"
)

func init() {
	// Keep test output free of verbose pipeline logs.
	log.SetOutput(io.Discard)
}

// newTestServer serves paginated HTML and JSON fixtures used by the block
// orchestration tests. Every page is recorded so tests can assert which
// requests actually happened (e.g. loop protection).
func newTestServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	requested := &[]string{}
	mux := http.NewServeMux()

	mux.HandleFunc("/html", func(w http.ResponseWriter, r *http.Request) {
		*requested = append(*requested, r.URL.String())
		page := pageValue(r.URL.Query().Get("page"))
		if page >= 2 {
			// Final page: no next link.
			fmt.Fprintf(w, `<html><body><h1>html-%d</h1></body></html>`, page)
			return
		}
		fmt.Fprintf(w, `<html><body><h1>html-%d</h1>
			<a href="/html?page=%d">next</a></body></html>`, page, page+1)
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		*requested = append(*requested, r.URL.String())
		page := pageValue(r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"page": page,
			"next": map[string]int{"page": page + 1},
		})
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		*requested = append(*requested, r.URL.String())
		// Always points back at itself: following it would loop forever.
		fmt.Fprint(w, `<html><body><h1>loop</h1><a href="/loop">next</a></body></html>`)
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, r *http.Request) {
		*requested = append(*requested, r.URL.String())
		// No matching element, but a working next link.
		fmt.Fprint(w, `<html><body><a href="/empty2">next</a></body></html>`)
	})
	mux.HandleFunc("/empty2", func(w http.ResponseWriter, r *http.Request) {
		*requested = append(*requested, r.URL.String())
		fmt.Fprint(w, `<html><body><h1>empty-2</h1></body></html>`)
	})
	mux.HandleFunc("/abs", func(w http.ResponseWriter, r *http.Request) {
		*requested = append(*requested, r.URL.String())
		fmt.Fprintf(w, `<html><body><h1>abs-1</h1><a href="%s/abs2">next</a></body></html>`,
			"http://"+r.Host)
	})
	mux.HandleFunc("/abs2", func(w http.ResponseWriter, r *http.Request) {
		*requested = append(*requested, r.URL.String())
		fmt.Fprint(w, `<html><body><h1>abs-2</h1></body></html>`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, requested
}

func pageValue(page string) int {
	if page == "" {
		return 1
	}
	var n int
	fmt.Sscanf(page, "%d", &n)
	if n == 0 {
		return 1
	}
	return n
}

func curlBlock(srv *httptest.Server, path string, queries []string, nextPage string) common.Block {
	return common.Block{
		Type:     "curl",
		Command:  fmt.Sprintf("curl %s%s", srv.URL, path),
		Queries:  queries,
		NextPage: nextPage,
	}
}

func runBlocks(t *testing.T, blocks []common.Block, maxPages int) *common.PipetApp {
	t.Helper()
	a := &common.PipetApp{Blocks: blocks, MaxPages: maxPages}
	err := ExecuteBlocks(a)
	assert.NoError(t, err)
	return a
}

// TestExecuteBlocksRelativePagination follows a relative next-page link and
// commits every completed page.
func TestExecuteBlocksRelativePagination(t *testing.T) {
	srv, requested := newTestServer(t)
	a := runBlocks(t, []common.Block{
		curlBlock(srv, "/html?page=1", []string{"h1"}, "a"),
	}, 3)

	assert.Len(t, a.Results, 1)
	r := a.Results[0]
	assert.Equal(t, StopReasonCompleted, r.StopReason)
	assert.Nil(t, r.Error)
	assert.Len(t, r.Pages, 2)
	assert.Equal(t, []interface{}{"html-1"}, r.Pages[0])
	assert.Equal(t, []interface{}{"html-2"}, r.Pages[1])
	assert.Equal(t, []string{"/html?page=1", "/html?page=2"}, *requested)
	// Flat Data keeps the historical layout used by text/template output.
	assert.Len(t, a.Data, 2)
}

// TestExecuteBlocksAbsolutePagination follows an absolute next-page URL.
func TestExecuteBlocksAbsolutePagination(t *testing.T) {
	srv, requested := newTestServer(t)
	a := runBlocks(t, []common.Block{
		curlBlock(srv, "/abs", []string{"h1"}, "a"),
	}, 3)

	r := a.Results[0]
	assert.Equal(t, StopReasonCompleted, r.StopReason)
	assert.Len(t, r.Pages, 2)
	assert.Equal(t, []interface{}{"abs-1"}, r.Pages[0])
	assert.Equal(t, []interface{}{"abs-2"}, r.Pages[1])
	assert.Equal(t, []string{"/abs", "/abs2"}, *requested)
}

// TestExecuteBlocksMaxPages caps pages at the actual number completed.
func TestExecuteBlocksMaxPages(t *testing.T) {
	srv, requested := newTestServer(t)
	a := runBlocks(t, []common.Block{
		curlBlock(srv, "/html?page=1", []string{"h1"}, "a"),
	}, 1)

	r := a.Results[0]
	assert.Equal(t, StopReasonMaxPages, r.StopReason)
	assert.Len(t, r.Pages, 1)
	assert.Equal(t, []string{"/html?page=1"}, *requested)
}

// TestExecuteBlocksLoopProtection stops when the next page resolves to an
// already visited URL, and keeps the single completed page without
// re-requesting it.
func TestExecuteBlocksLoop(t *testing.T) {
	srv, requested := newTestServer(t)
	a := runBlocks(t, []common.Block{
		curlBlock(srv, "/loop", []string{"h1"}, "a"),
	}, 10)

	r := a.Results[0]
	assert.Equal(t, StopReasonLoop, r.StopReason)
	assert.Nil(t, r.Error)
	assert.Len(t, r.Pages, 1)
	assert.Equal(t, []string{"/loop"}, *requested)
}

// TestExecuteBlocksEmptyPage still counts the empty page as completed and
// continues to the next one.
func TestExecuteBlocksEmptyPage(t *testing.T) {
	srv, requested := newTestServer(t)
	a := runBlocks(t, []common.Block{
		curlBlock(srv, "/empty", []string{"h1"}, "a"),
	}, 3)

	r := a.Results[0]
	assert.Equal(t, StopReasonCompleted, r.StopReason)
	assert.Len(t, r.Pages, 2)
	assert.Equal(t, []interface{}{""}, r.Pages[0])
	assert.Equal(t, []interface{}{"empty-2"}, r.Pages[1])
	assert.Equal(t, []string{"/empty", "/empty2"}, *requested)
}

// TestExecuteBlocksJSONPagination exercises the JSON path with a relative
// next page.
func TestExecuteBlocksJSONPagination(t *testing.T) {
	srv, requested := newTestServer(t)
	block := common.Block{
		Type:     "curl",
		Command:  fmt.Sprintf("curl %s/json?page=1", srv.URL),
		Queries:  []string{"page"},
		NextPage: "a", // ignored for JSON: no next page URL is produced
	}
	a := runBlocks(t, []common.Block{block}, 3)

	r := a.Results[0]
	assert.Equal(t, StopReasonCompleted, r.StopReason)
	assert.Len(t, r.Pages, 1)
	assert.Equal(t, []interface{}{float64(1)}, r.Pages[0])
	assert.Equal(t, []string{"/json?page=1"}, *requested)
}

// TestExecuteBlocksFailingPipe stops the block at the failing page and keeps
// the pages completed before it.
func TestExecuteBlocksFailingPipe(t *testing.T) {
	srv, _ := newTestServer(t)
	block := common.Block{
		Type:     "curl",
		Command:  fmt.Sprintf("curl %s/html?page=1", srv.URL),
		Queries:  []string{"h1 | command-that-does-not-exist-12345"},
		NextPage: "a",
	}
	a := runBlocks(t, []common.Block{block}, 3)

	r := a.Results[0]
	assert.Equal(t, StopReasonError, r.StopReason)
	if assert.NotNil(t, r.Error) {
		assert.Equal(t, 1, r.Error.Page)
		assert.Equal(t, "pipe", r.Error.Stage)
	}
	// The failing page is not committed.
	assert.Empty(t, r.Pages)
	assert.Empty(t, a.Data)
}

// TestExecuteBlocksCommandFailure uses a fake curl that exits non-zero: the
// block stops with a "command" stage error and no page is committed.
func TestExecuteBlocksCommandFailure(t *testing.T) {
	dir := t.TempDir()
	fakeCurl := filepath.Join(dir, "curl")
	err := os.WriteFile(fakeCurl, []byte("#!/bin/sh\necho 'curl: simulated failure' >&2\nexit 7\n"), 0o755)
	assert.NoError(t, err)

	block := common.Block{
		Type:    "curl",
		Command: fmt.Sprintf("%s http://example.com", fakeCurl),
		Queries: []string{"h1"},
	}
	a := runBlocks(t, []common.Block{block}, 3)

	r := a.Results[0]
	assert.Equal(t, StopReasonError, r.StopReason)
	if assert.NotNil(t, r.Error) {
		assert.Equal(t, 1, r.Error.Page)
		assert.Equal(t, "command", r.Error.Stage)
		assert.Contains(t, r.Error.Message, "curl command failed")
	}
	assert.Empty(t, r.Pages)
}

// TestExecuteBlocksMultiBlockPartialFailure verifies that a failing block is
// isolated: its earlier pages remain, and the other blocks still execute.
func TestExecuteBlocksMultiBlockPartialFailure(t *testing.T) {
	srv, _ := newTestServer(t)
	good := curlBlock(srv, "/html?page=1", []string{"h1"}, "a")
	bad := common.Block{
		Type:     "curl",
		Command:  fmt.Sprintf("curl %s/html?page=1", srv.URL),
		Queries:  []string{"h1 | command-that-does-not-exist-12345"},
		NextPage: "a",
	}
	alsoGood := curlBlock(srv, "/empty", []string{"h1"}, "")

	a := runBlocks(t, []common.Block{good, bad, alsoGood}, 3)

	assert.Len(t, a.Results, 3)

	assert.Equal(t, StopReasonCompleted, a.Results[0].StopReason)
	assert.Len(t, a.Results[0].Pages, 2)

	assert.Equal(t, StopReasonError, a.Results[1].StopReason)
	assert.NotNil(t, a.Results[1].Error)
	assert.Empty(t, a.Results[1].Pages)

	assert.Equal(t, StopReasonCompleted, a.Results[2].StopReason)
	assert.Len(t, a.Results[2].Pages, 1)

	// Only the successful pages land in the flat Data slice.
	assert.Len(t, a.Data, 3)
	errs := a.BlockErrors()
	assert.Len(t, errs, 1)
	assert.Equal(t, 1, errs[0].Block)
}

// TestExecuteBlocksRerunDoesNotDuplicate ensures a second run over the same
// pages visits each URL once and does not duplicate committed data.
func TestExecuteBlocksRerunDoesNotDuplicate(t *testing.T) {
	srv, requested := newTestServer(t)
	blocks := []common.Block{curlBlock(srv, "/loop", []string{"h1"}, "a")}

	first := runBlocks(t, blocks, 10)
	assert.Len(t, first.Results[0].Pages, 1)

	// A fresh PipetApp simulates the --interval rerun: the original command
	// is untouched, and the loop protection still prevents duplicate work.
	second := runBlocks(t, blocks, 10)
	assert.Len(t, second.Results[0].Pages, 1)
	assert.Equal(t, StopReasonLoop, second.Results[0].StopReason)
	// Each run requested /loop exactly once.
	assert.Equal(t, []string{"/loop", "/loop"}, *requested)
}

// TestBlockURLAndAdvanceURL covers relative and absolute reference
// resolution plus the no-URL error path without involving HTTP.
func TestBlockURLAndAdvanceURL(t *testing.T) {
	cmd := "curl https://example.com/a/b?x=1 -H 'Accept: text/html'"

	assert.Equal(t, "https://example.com/a/b?x=1", blockURL(cmd))

	next, err := advanceURL(cmd, "/c/d")
	assert.NoError(t, err)
	parts, ok := next.([]string)
	assert.True(t, ok)
	assert.Equal(t, "https://example.com/c/d", parts[1])

	next, err = advanceURL(cmd, "https://other.test/z")
	assert.NoError(t, err)
	assert.Equal(t, "https://other.test/z", next.([]string)[1])

	_, err = advanceURL("curl --help", "/next")
	assert.Error(t, err)
}

// TestParseSpecFileCoversBlocksAndNextPage checks parsing stays compatible
// with the existing spec syntax, including comments and blank separators.
func TestParseSpecFileCoversBlocksAndNextPage(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "sample.pipet")
	content := `// a comment
curl https://example.com/
h1
> a.next

playwright https://example.com/app
document.title
`
	err := os.WriteFile(spec, []byte(content), 0o644)
	assert.NoError(t, err)

	a := &common.PipetApp{}
	assert.NoError(t, ParseSpecFile(a, spec))
	assert.Len(t, a.Blocks, 2)
	assert.Equal(t, "curl", a.Blocks[0].Type)
	assert.Equal(t, []string{"h1"}, a.Blocks[0].Queries)
	assert.Equal(t, " a.next", a.Blocks[0].NextPage)
	assert.Equal(t, "playwright", a.Blocks[1].Type)
}
