// Package prompts embeds the pipeline's prompt templates.
//
// The templates live at the repository root rather than beside the code that renders
// them, for two reasons. A reviewer looking for "what did you actually ask the model"
// should find it without reading Go, and prompt iteration — which is most of the real
// engineering in a pipeline like this — shows up as a reviewable diff in git history
// instead of disappearing inside a code change.
//
// This file exists only so go:embed can reach the templates, since embed cannot see
// outside its own package directory.
package prompts

import "embed"

// FS holds the prompt templates, named "<role>_<stage>.md.tmpl".
//
//go:embed *.md.tmpl
var FS embed.FS
