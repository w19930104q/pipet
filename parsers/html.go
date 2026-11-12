package parsers

import (
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/bjesus/pipet/common"
)

// validateSelector makes invalid CSS selectors fail explicitly. goquery
// swallows the parser error from cascadia and returns an empty selection,
// which would otherwise look like a legitimate empty match.
func validateSelector(selector string) error {
	if _, err := cascadia.Parse(selector); err != nil {
		return common.NewStageError("parse",
			fmt.Errorf("invalid CSS selector %q: %w", selector, err))
	}
	return nil
}

func ParseHTMLQueries(htmlData []byte, queries []string, nextPage string) (result []interface{}, nextPageURL string, err error) {
	// Invalid CSS selectors make goquery panic - turn them into a parse
	// error so the block is stopped instead of crashing the whole run.
	defer func() {
		if r := recover(); r != nil {
			err = common.NewStageError("parse", fmt.Errorf("HTML query failed: %v", r))
			result = nil
			nextPageURL = ""
		}
	}()

	result = []interface{}{}

	// get new HTML
	doc, docErr := goquery.NewDocumentFromReader(strings.NewReader(string(htmlData)))
	if docErr != nil {
		return nil, "", common.NewStageError("parse", fmt.Errorf("failed to parse HTML: %w", docErr))
	}

	for i, line := range queries {

		indentation := CalculateIndentation(line)
		if len(queries) > i+1 && CalculateIndentation(queries[i+1]) > indentation {
			// if line has children
			if err := validateSelector(line); err != nil {
				return nil, "", err
			}
			elements := doc.Find(line)

			// get new lines
			var lines []string

			for subi := i + 1; subi < len(queries); subi++ {
				if CalculateIndentation(queries[subi]) > indentation {
					child := queries[subi][2:]
					if err := validateSelector(child); err != nil {
						return nil, "", err
					}
					lines = append(lines, child)
				} else {
					break
				}
			}

			subresult := []interface{}{}

			var subErr error
			elements.Each(func(subi int, subdoc *goquery.Selection) {
				if subErr != nil {
					return
				}
				html, _ := goquery.OuterHtml(subdoc)

				if strings.HasPrefix(html, "<tr") || strings.HasPrefix(html, "<td") {
					html = "<table>" + html + "</table>"
				}
				value2, _, e := ParseHTMLQueries([]byte(html), lines, "")
				if e != nil {
					subErr = e
					return
				}

				if len(value2) == 1 {
					subresult = append(subresult, value2[0])
				} else if len(value2) > 1 {
					subresult = append(subresult, value2)
				}

			})
			if subErr != nil {
				return nil, "", subErr
			}

			// Only append non-empty results
			if len(subresult) > 0 {
				result = append(result, subresult)
			}
		} else if indentation == 0 {
			parts := strings.Split(line, "|")
			selector := strings.TrimSpace(parts[0])
			if err := validateSelector(selector); err != nil {
				return nil, "", err
			}
			elements := doc.Find(selector)
			value := elements.First()

			html := ""

			if len(parts) > 1 {
				html, _ = goquery.OuterHtml(value)
				for _, pipe := range parts[1:] {
					pipedValue, e := ExecutePipe(html, strings.TrimSpace(pipe))
					if e != nil {
						return nil, "", e
					}
					html = strings.TrimRight(pipedValue, "\n")
				}
			} else {
				html = value.Text()
			}

			result = append(result, html)
		}

	}

	if nextPage != "" {
		if err := validateSelector(nextPage); err != nil {
			return nil, "", err
		}
		nextPageURL = doc.Find(nextPage).First().AttrOr("href", "")
	}

	return result, nextPageURL, nil
}
