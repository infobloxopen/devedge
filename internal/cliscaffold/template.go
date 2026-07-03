package cliscaffold

import (
	"fmt"
	"text/template"
)

// parsePresetTemplate parses a preset overlay template with the same options the
// base tree uses (missingkey=error), so an overlay that references an unknown
// field fails loud instead of emitting "<no value>".
func parsePresetTemplate(name, body string) (*template.Template, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parsing preset template %s: %w", name, err)
	}
	return t, nil
}
