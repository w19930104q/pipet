package parsers

import (
	"testing"

	"github.com/bjesus/pipet/common"
	"github.com/stretchr/testify/assert"
)

func TestParseHTMLQueries(t *testing.T) {
	htmlData := []byte(`
		<ul><li>one</li><li>two</li><li>three</li></ul>
		<p><ol><li>foo</li><li>bar</li></ol>
		<table>
			<tr><td>penny</td><td>lane</td></tr>
			<tr><td>strawberry</td><td>fields</td></tr>
			<tr><td>everybody's got something to hide</td><td>except for me and my monkey</td></tr>
		</table>
		<table>
			<tr><td>fool</td><td>on</td><td>the</td><td>hill</td></tr>
		</table>
		<a href="/more">next</a>
	`)

	queries := []string{
		"ul li",
		"  li",
		"ol li",
		"ol li | wc -c",
		"table",
		"  tr",
		"    td",
		"      td",
	}

	result, nextPage, err := ParseHTMLQueries(htmlData, queries, "a")

	expectedResult := []interface{}{[]interface{}{"one", "two", "three"}, "foo", "12", []interface{}{[]interface{}{[]interface{}{"penny", "lane"}, []interface{}{"strawberry", "fields"}, []interface{}{"everybody's got something to hide", "except for me and my monkey"}}, []interface{}{[]interface{}{"fool", "on", "the", "hill"}}}}

	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
	assert.Equal(t, "/more", nextPage)
}

// TestParseHTMLQueriesBadSelector turns an invalid CSS selector into a parse
// error instead of panicking the whole process.
func TestParseHTMLQueriesBadSelector(t *testing.T) {
	htmlData := []byte(`<html><body><h1>title</h1></body></html>`)

	result, nextPage, err := ParseHTMLQueries(htmlData, []string{"h1 >>> ["}, "")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "", nextPage)
	var stageErr *common.StageError
	assert.ErrorAs(t, err, &stageErr)
	assert.Equal(t, "parse", stageErr.Stage)
}

// TestParseHTMLQueriesFailingPipe propagates a non-zero pipe exit instead of
// silently emitting an empty value.
func TestParseHTMLQueriesFailingPipe(t *testing.T) {
	htmlData := []byte(`<html><body><h1>title</h1></body></html>`)

	result, _, err := ParseHTMLQueries(htmlData,
		[]string{"h1 | command-that-does-not-exist-12345"}, "")

	assert.Error(t, err)
	assert.Nil(t, result)
	var stageErr *common.StageError
	assert.ErrorAs(t, err, &stageErr)
	assert.Equal(t, "pipe", stageErr.Stage)
}

// TestParseHTMLQueriesEmptyMatchPage verifies that a page with no matching
// elements is still a completed page: it returns the empty string for the
// unmatched query and no error, so pagination bookkeeping counts it.
func TestParseHTMLQueriesEmptyMatchPage(t *testing.T) {
	htmlData := []byte(`<html><body><a href="/p2">next</a></body></html>`)

	result, nextPage, err := ParseHTMLQueries(htmlData, []string{"h1"}, "a")

	assert.NoError(t, err)
	assert.Equal(t, []interface{}{""}, result)
	assert.Equal(t, "/p2", nextPage)
}

// TestParseHTMLQueriesWhitespaceAroundPipe keeps the historical tolerance of
// whitespace around a query and its pipe.
func TestParseHTMLQueriesWhitespaceAroundPipe(t *testing.T) {
	htmlData := []byte(`<html><body><h1>title</h1></body></html>`)

	result, _, err := ParseHTMLQueries(htmlData,
		[]string{"h1 | wc -c"}, "")

	assert.NoError(t, err)
	// The pipe receives the element's outer HTML: <h1>title</h1>.
	assert.Equal(t, []interface{}{"14"}, result)
}

// TestParseHTMLQueriesMissingNextLink returns an empty next page URL when
// the selector does not match, which ends pagination normally.
func TestParseHTMLQueriesMissingNextLink(t *testing.T) {
	htmlData := []byte(`<html><body><h1>title</h1></body></html>`)

	result, nextPage, err := ParseHTMLQueries(htmlData, []string{"h1"}, "a.next")

	assert.NoError(t, err)
	assert.Equal(t, []interface{}{"title"}, result)
	assert.Equal(t, "", nextPage)
}
