package common

// StageError carries the pipeline stage a failure happened in so the
// application layer can report it without parsing error strings.
type StageError struct {
	Stage string
	Err   error
}

func (e *StageError) Error() string { return e.Err.Error() }
func (e *StageError) Unwrap() error { return e.Err }

// NewStageError wraps err, tagging it with the failing stage.
func NewStageError(stage string, err error) *StageError {
	return &StageError{Stage: stage, Err: err}
}

// BlockError describes the failure that stopped a block, together with the
// 1-based page number on which it occurred and the stage that failed
// ("command", "parse", "pipe" or "next-page").
type BlockError struct {
	Block   int    `json:"block"`
	Page    int    `json:"page"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// BlockResult is the outcome of a single block from the spec file. Pages only
// contains pages that were completed successfully - a failing page is never
// appended, so no partial records leak into the output.
type BlockResult struct {
	Index      int           `json:"index"`
	Type       string        `json:"type"`
	Pages      []interface{} `json:"pages"`
	StopReason string        `json:"stop_reason"`
	Error      *BlockError   `json:"error,omitempty"`
}

type Block struct {
	Type     string
	Command  interface{}
	Queries  []string
	NextPage string
}

type PipetApp struct {
	Blocks []Block
	// Data keeps the historical flat layout (one entry per successfully
	// completed page, in block order) used by the text and template outputs.
	Data      []interface{}
	Results   []BlockResult
	MaxPages  int
	Separator []string
}

// BlockErrors returns the errors of all failed blocks in execution order.
func (a *PipetApp) BlockErrors() []*BlockError {
	var errs []*BlockError
	for i := range a.Results {
		if a.Results[i].Error != nil {
			errs = append(errs, a.Results[i].Error)
		}
	}
	return errs
}
