package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/bjesus/pipet/common"
	"github.com/bjesus/pipet/internal/app"
	"github.com/bjesus/pipet/outputs"
	"github.com/bjesus/pipet/utils"
)

// errPartialFailure signals that at least one block failed while others may
// have completed. The per-block details were already printed on stderr, so
// main() only needs to turn this into a non-zero exit code.
var errPartialFailure = errors.New("one or more blocks failed")

func main() {
	log.SetFlags(log.Lshortfile | log.Ltime)
	cli.VersionFlag = &cli.BoolFlag{
		Name:  "version",
		Usage: "print the pipet version",
	}
	app := &cli.App{
		Name:                   "pipet",
		Usage:                  "swiss-army tool for web scraping, made for hackers",
		HideHelpCommand:        true,
		UseShortOptionHandling: true,
		EnableBashCompletion:   true,
		Version:                "0.3.0",
		ArgsUsage:              "<pipet_file>",

		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "json",
				Aliases: []string{"j"},
				Usage:   "output as JSON",
			},
			&cli.StringFlag{
				Name:    "template",
				Aliases: []string{"t"},
				Usage:   "path to file for template output",
			},
			&cli.StringSliceFlag{
				Name:    "separator",
				Aliases: []string{"s"},
				Usage:   "set a separator for text output (can be used multiple times)",
			},
			&cli.IntFlag{
				Name:    "max-pages",
				Value:   3,
				Aliases: []string{"p"},
				Usage:   "maximum number of pages to scrape",
			},
			&cli.IntFlag{
				Name:    "interval",
				Value:   0,
				Aliases: []string{"i"},
				Usage:   "rerun pipet after X seconds, 0 to disable",
			},
			&cli.StringFlag{
				Name:    "on-change",
				Aliases: []string{"c"},
				Usage:   "a command to run when the pipet result is new",
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "enable verbose logging",
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() == 0 {
				return fmt.Errorf("pipet file argument is required")
			}
			spec := c.Args().Get(0)
			return runPipet(c, spec)
		},
	}

	if err := app.Run(os.Args); err != nil {
		switch {
		case errors.Is(err, outputs.ErrBrokenPipe):
			// The downstream consumer (e.g. `head`) went away. This is a
			// normal way for a piped command to end, so leave quietly.
			os.Exit(0)
		case errors.Is(err, errPartialFailure):
			os.Exit(1)
		}
		log.Println("pipet version", app.Version)
		log.Fatal(err)
	}
}

func runPipet(c *cli.Context, specFile string) error {
	jsonOutput := c.Bool("json")
	separators := c.StringSlice("separator")
	templateFile := c.String("template")
	onChange := c.String("on-change")
	maxPages := c.Int("max-pages")
	interval := c.Int("interval")
	verbose := c.Bool("verbose")

	if !verbose {
		log.SetOutput(io.Discard)
	}

	automaticTemplateFile := strings.TrimSuffix(specFile, filepath.Ext(specFile)) + ".tpl"

	if !jsonOutput && templateFile == "" && utils.FileExists(automaticTemplateFile) {
		log.Println("Detected template file at", specFile)
		templateFile = automaticTemplateFile
	}

	pipet := &common.PipetApp{
		MaxPages:  maxPages,
		Separator: separators,
	}

	log.Println("Parsing pipet file:", specFile)
	err := app.ParseSpecFile(pipet, specFile)
	if err != nil {
		return fmt.Errorf("error parsing spec file: %w", err)
	}

	iterate := true
	previousValue := ""

	for iterate {
		newValue := ""
		log.Println("Executing blocks")
		err = app.ExecuteBlocks(pipet)
		if err != nil {
			return fmt.Errorf("error executing blocks: %w", err)
		}

		// Report block failures on stderr while still emitting the data of
		// the blocks and pages that did complete.
		for _, blockErr := range pipet.BlockErrors() {
			fmt.Fprintf(os.Stderr,
				"pipet: block %d stopped at page %d (%s): %s\n",
				blockErr.Block, blockErr.Page, blockErr.Stage, blockErr.Message)
		}

		log.Println("Generating output")

		if jsonOutput {
			newValue, err = outputs.OutputJSON(pipet)
			if err != nil {
				return fmt.Errorf("error generating JSON output: %w", err)
			}
		} else if templateFile != "" {
			newValue, err = outputs.OutputTemplate(pipet, templateFile)
			if err != nil {
				return err
			}
		} else {
			newValue = outputs.OutputText(pipet)
		}

		if writeErr := outputs.Write(os.Stdout, newValue); writeErr != nil {
			return writeErr
		}

		// A failed block is reported immediately, even in interval mode, so
		// the exit status cannot silently stay zero.
		if len(pipet.BlockErrors()) > 0 {
			return errPartialFailure
		}

		if interval > 0 {
			if onChange != "" && previousValue != newValue {
				command := strings.ReplaceAll(onChange, "{}", strconv.Quote(newValue))
				log.Println("Executing on change command: " + command)
				cmd := exec.Command("bash", "-c", command)
				cmd.Output()
				previousValue = newValue
			}
			pipet.Data = []interface{}{}
			pipet.Results = []common.BlockResult{}
			time.Sleep(time.Duration(interval) * time.Second)
		} else {
			iterate = false
		}
	}

	return nil
}
