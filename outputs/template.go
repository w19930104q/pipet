package outputs

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"syscall"

	"github.com/bjesus/pipet/common"
)

// ErrBrokenPipe is returned by Write when the consumer closed the reading
// end of the pipe. Callers should treat it as a silent, successful stop.
var ErrBrokenPipe = errors.New("broken pipe")

// Write writes content to w. A write that fails because the reader went away
// (SIGPIPE/EPIPE, an already-closed pipe or file) is normalized to
// ErrBrokenPipe.
func Write(w io.Writer, content string) error {
	_, err := io.WriteString(w, content)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, os.ErrClosed) {
		return ErrBrokenPipe
	}
	return err
}

// OutputTemplate renders the given template with the historically flat
// app.Data. A missing or invalid template is now reported instead of
// silently producing empty output.
func OutputTemplate(app *common.PipetApp, templateFile string) (string, error) {
	tmpl, err := template.ParseFiles(templateFile)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %q: %w", templateFile, err)
	}
	var doc bytes.Buffer
	if err := tmpl.Execute(&doc, app.Data); err != nil {
		return "", fmt.Errorf("failed to execute template %q: %w", templateFile, err)
	}
	return doc.String(), nil
}
