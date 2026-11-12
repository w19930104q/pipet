package parsers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bjesus/pipet/common"
	"github.com/tidwall/gjson"
)

func ParseJSONQueries(jsonData []byte, queries []string) (result []interface{}, nextPage string, err error) {
	// GJSON normally tolerates malformed paths, but keep a safety net so any
	// unexpected failure is reported rather than crashing the run.
	defer func() {
		if r := recover(); r != nil {
			err = common.NewStageError("parse", fmt.Errorf("JSON query failed: %v", r))
			result = nil
		}
	}()

	result = []interface{}{}

	for i, line := range queries {

		indentation := CalculateIndentation(line)
		if len(queries) > i+1 && CalculateIndentation(queries[i+1]) > indentation {
			// if line has children
			elements := gjson.GetBytes(jsonData, line)

			// get new lines
			var lines []string

			for subi := i + 1; subi < len(queries); subi++ {
				if CalculateIndentation(queries[subi]) > indentation {
					lines = append(lines, queries[subi][2:])
				} else {
					break
				}
			}

			subresult := []interface{}{}
			var subErr error
			elements.ForEach(func(subi gjson.Result, subdoc gjson.Result) bool {
				html := subdoc.String()

				value2, _, e := ParseJSONQueries([]byte(html), lines)
				if e != nil {
					subErr = e
					return false
				}
				subresult = append(subresult, value2)
				return true
			})
			if subErr != nil {
				return nil, "", subErr
			}

			result = append(result, subresult)
		} else if indentation == 0 {
			parts := strings.Split(line, "|")
			query := strings.TrimSpace(parts[0])
			value := gjson.GetBytes(jsonData, query)

			html := ""

			if len(parts) > 1 {
				html = value.String()
				for _, pipe := range parts[1:] {
					pipedValue, e := ExecutePipe(html, strings.TrimSpace(pipe))
					if e != nil {
						return nil, "", e
					}
					html = strings.TrimRight(pipedValue, "\n")
				}
			} else {
				html = value.String()
			}

			if json.Valid([]byte(html)) {
				var parsedJSON interface{}
				if e := json.Unmarshal([]byte(html), &parsedJSON); e != nil {
					return nil, "", common.NewStageError("parse",
						fmt.Errorf("failed to parse pipe output as JSON: %w", e))
				}
				result = append(result, parsedJSON)
			} else {
				result = append(result, html)
			}

		}

	}
	return result, "", nil
}
