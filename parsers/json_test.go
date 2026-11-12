package parsers

import (
	"testing"

	"github.com/bjesus/pipet/common"
	"github.com/stretchr/testify/assert"
)

func TestParseJSONQueries(t *testing.T) {
	JSONData := []byte(`
		{"person": {"name": "bjesus", "friends": [{ "name": "bob", "nickname": "rocky"}, {"name":"alice", "nickname": "racoon"}]}}
	`)

	queries := []string{
		"person.name",
		"person.name | wc -c",
		"person.friends",
		"  name",
		"  nickname",
	}

	result, nextPage, err := ParseJSONQueries(JSONData, queries)

	expectedResult := []interface{}{
		"bjesus",
		float64(6),
		[]interface{}{[]interface{}{"bob", "rocky"}, []interface{}{"alice", "racoon"}},
	}

	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
	assert.Equal(t, "", nextPage)
}

// TestParseJSONQueriesFailingPipe makes a non-zero pipe exit stop parsing
// with a "pipe" stage error instead of silently emitting an empty value.
func TestParseJSONQueriesFailingPipe(t *testing.T) {
	JSONData := []byte(`{"person": {"name": "bjesus"}}`)

	result, _, err := ParseJSONQueries(JSONData,
		[]string{"person.name | command-that-does-not-exist-12345"})

	assert.Error(t, err)
	assert.Nil(t, result)
	var stageErr *common.StageError
	assert.ErrorAs(t, err, &stageErr)
	assert.Equal(t, "pipe", stageErr.Stage)
}

// TestParseJSONQueriesMissingPath confirms a path with no match behaves as a
// normal query returning an empty string - not as an error.
func TestParseJSONQueriesMissingPath(t *testing.T) {
	JSONData := []byte(`{"person": {"name": "bjesus"}}`)

	result, nextPage, err := ParseJSONQueries(JSONData, []string{"person.age"})

	assert.NoError(t, err)
	assert.Equal(t, []interface{}{""}, result)
	assert.Equal(t, "", nextPage)
}

// TestParseJSONQueriesNestedPipeError propagates a pipe failure from inside
// a nested iteration rather than dropping the sub-result.
func TestParseJSONQueriesNestedPipeError(t *testing.T) {
	JSONData := []byte(`{"friends": [{"name": "bob"}, {"name": "alice"}]}`)

	result, _, err := ParseJSONQueries(JSONData, []string{
		"friends",
		"  name | command-that-does-not-exist-12345",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	var stageErr *common.StageError
	assert.ErrorAs(t, err, &stageErr)
	assert.Equal(t, "pipe", stageErr.Stage)
}
