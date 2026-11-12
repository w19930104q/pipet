package outputs

import (
	"encoding/json"

	"github.com/bjesus/pipet/common"
)

// JSONReport is the stable --json envelope. Successful results live in
// "results"; failed blocks are listed separately in "errors", so no partial
// record is ever mixed into the result data.
type JSONReport struct {
	Results []common.BlockResult `json:"results"`
	Errors  []*common.BlockError `json:"errors"`
}

// OutputJSON renders the stable results/errors report. Both fields are
// always arrays, so consumers can iterate over them without null checks, and
// no partial record is ever mixed into the result data.
func OutputJSON(app *common.PipetApp) (string, error) {
	results := app.Results
	if results == nil {
		results = []common.BlockResult{}
	}
	errs := app.BlockErrors()
	if errs == nil {
		errs = []*common.BlockError{}
	}
	report := JSONReport{
		Results: results,
		Errors:  errs,
	}
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
}
