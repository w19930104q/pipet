package app

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/bjesus/pipet/common"
	"github.com/bjesus/pipet/parsers"
	"github.com/google/shlex"
)

// Stop reasons reported per block in the JSON output.
const (
	StopReasonCompleted = "completed"
	StopReasonMaxPages  = "max-pages"
	StopReasonLoop      = "loop"
	StopReasonError     = "error"
)

func ParseSpecFile(e *common.PipetApp, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var currentBlock *common.Block

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if currentBlock != nil {
				e.Blocks = append(e.Blocks, *currentBlock)
				currentBlock = nil
			}
			continue
		}

		if strings.HasPrefix(line, "//") {
			continue
		}
		if currentBlock == nil {
			if strings.HasPrefix(line, "curl ") {
				currentBlock = &common.Block{Type: "curl", Command: line}
			} else if strings.HasPrefix(line, "playwright ") {
				currentBlock = &common.Block{Type: "playwright", Command: line}
			} else {
				return fmt.Errorf("invalid block start: %s", line)
			}
		} else {
			if strings.HasPrefix(line, "> ") {

				currentBlock.NextPage = strings.TrimPrefix(line, ">")
			} else {
				currentBlock.Queries = append(currentBlock.Queries, line)
			}
		}
	}

	if currentBlock != nil {
		e.Blocks = append(e.Blocks, *currentBlock)
	}

	return scanner.Err()
}

// ExecuteBlocks runs every block independently. A failing command or pipe
// stops only the block it belongs in; the pages already completed by that
// block are kept and the remaining blocks still run. Per-block outcomes are
// stored in e.Results; successfully completed pages are also appended to
// e.Data in block order, preserving the historical text/template layout.
func ExecuteBlocks(e *common.PipetApp) error {
	for blockIndex, block := range e.Blocks {
		result := common.BlockResult{
			Index: blockIndex,
			Type:  block.Type,
			Pages: []interface{}{},
		}

		// Work on a local copy so a rerun (--interval) starts from the
		// original command again.
		workingBlock := block
		visited := map[string]bool{}
		reason := StopReasonCompleted

		for page := 1; page <= e.MaxPages; page++ {
			var data interface{}
			var nextPageURL string
			var execErr error

			currentURL := blockURL(workingBlock.Command)
			if currentURL != "" {
				if visited[currentURL] {
					reason = StopReasonLoop
					log.Printf("block %d: loop detected at %s, stopping after %d completed page(s)",
						blockIndex, currentURL, len(result.Pages))
					break
				}
				visited[currentURL] = true
			}

			if workingBlock.Type == "curl" {
				data, nextPageURL, execErr = parsers.ExecuteCurlBlock(workingBlock)
			} else if workingBlock.Type == "playwright" {
				data, execErr = parsers.ExecutePlaywrightBlock(workingBlock)
			} else {
				execErr = fmt.Errorf("unknown block type %q", workingBlock.Type)
			}

			if execErr != nil {
				reason = StopReasonError
				stage := "command"
				var stageErr *common.StageError
				if errors.As(execErr, &stageErr) {
					stage = stageErr.Stage
				}
				result.Error = &common.BlockError{
					Block:   blockIndex,
					Page:    page,
					Stage:   stage,
					Message: execErr.Error(),
				}
				log.Printf("block %d page %d: %s", blockIndex, page, execErr.Error())
				break
			}

			// The page completed successfully - commit it. A page returning
			// no query matches still counts as a completed page.
			result.Pages = append(result.Pages, data)
			e.Data = append(e.Data, data)

			if nextPageURL == "" {
				reason = StopReasonCompleted
				break
			}

			next, err := advanceURL(workingBlock.Command, nextPageURL)
			if err != nil {
				reason = StopReasonError
				result.Error = &common.BlockError{
					Block:   blockIndex,
					Page:    page,
					Stage:   "next-page",
					Message: err.Error(),
				}
				log.Printf("block %d page %d: %s", blockIndex, page, err.Error())
				break
			}
			workingBlock.Command = next

			if page == e.MaxPages {
				reason = StopReasonMaxPages
			}
		}

		if e.MaxPages <= 0 {
			reason = StopReasonMaxPages
		}

		result.StopReason = reason
		e.Results = append(e.Results, result)
	}

	return nil
}

// blockURL extracts the first http(s) argument of a block command, if any.
func blockURL(command interface{}) string {
	parts := splitCommand(command)
	for _, u := range parts {
		if len(u) >= 4 && u[:4] == "http" {
			return u
		}
	}
	return ""
}

// advanceURL replaces the first http(s) argument in command with the next
// page reference resolved against the current URL. Both absolute and
// relative references are supported.
func advanceURL(command interface{}, nextPageURL string) (interface{}, error) {
	parts := splitCommand(command)
	for i, u := range parts {
		if len(u) >= 4 && u[:4] == "http" {
			resolved, err := concatenateURLs(u, nextPageURL)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve next page %q against %q: %w", nextPageURL, u, err)
			}
			parts[i] = resolved
			return parts, nil
		}
	}
	return nil, fmt.Errorf("no URL found in block command, cannot follow next page %q", nextPageURL)
}

func splitCommand(command interface{}) []string {
	switch cmd := command.(type) {
	case string:
		parts, err := shlex.Split(cmd)
		if err != nil {
			log.Printf("failed to parse block command %q: %s", cmd, err)
			return nil
		}
		return parts
	case []string:
		return cmd
	default:
		return nil
	}
}

func concatenateURLs(base, ref string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", err
	}

	// Resolve reference URL relative to the base URL
	fullURL := baseURL.ResolveReference(refURL)

	return fullURL.String(), nil
}
