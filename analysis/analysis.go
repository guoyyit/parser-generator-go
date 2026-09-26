// Package analysis normalizes grammar definitions and computes the sets used
// to report decidable grammar conflicts.
package analysis

import "github.com/guoyyit/parser-generator-go/grammar"

// Conflict describes a decidable ambiguity or overlap in the grammar.
type Conflict struct {
	Pos     grammar.Position
	Rule    string
	Message string
}

func (c *Conflict) Error() string {
	if c.Pos.Line > 0 || c.Pos.File != "" {
		return c.Pos.String() + ": " + c.Message
	}
	return c.Message
}

// EOF is the synthetic terminal representing end of input; it appears in
// FOLLOW sets only.

// EOF is the synthetic terminal representing end of input; it appears in
// FOLLOW sets only.
const EOF = "$EOF"
