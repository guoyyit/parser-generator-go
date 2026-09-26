// Package generator turns grammar definitions into reusable, immutable
// parsers. A Generator is safe for concurrent use and may be reused to
// compile many grammar definitions.
package generator

import (
	"errors"
	"fmt"

	"github.com/guoyyit/parser-generator-go/analysis"
	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/lexer"
	"github.com/guoyyit/parser-generator-go/runtime"
	"github.com/guoyyit/parser-generator-go/version"
)

// Generator is a concurrency-safe, reusable parser generator. It holds no
// mutable state: Generate operates only on its immutable argument.
type Generator struct{}

// New creates a generator.
func New() *Generator { return &Generator{} }

// CompiledGrammar bundles the immutable artifacts produced from a definition.
// All fields are read-only after creation and may be shared by goroutines.
type CompiledGrammar struct {
	Definition *grammar.Grammar
	Lexer      *lexer.Compiled
	Normalized *analysis.NormalGrammar
	Parser     *runtime.Parser
	Signature  string
	Version    string
}

// Generate validates, analyzes and compiles a grammar definition. Any
// duplicate/conflicting declaration or unresolved grammar ambiguity is
// returned as an error and no parser is produced.
func (g *Generator) Generate(def *grammar.Grammar) (*CompiledGrammar, error) {
	if def == nil {
		return nil, errors.New("generator: nil grammar definition")
	}
	if err := def.Validate(); err != nil {
		return nil, err
	}
	lx, err := lexer.Compile(def.Tokens)
	if err != nil {
		return nil, err
	}
	ng, err := analysis.Normalize(def)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, c := range ng.Conflicts() {
		cc := c
		errs = append(errs, &cc)
	}
	if a := ng.BoundedAmbiguity(); a != nil {
		errs = append(errs, fmt.Errorf("%s: %s", a.Rule, a.Message))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &CompiledGrammar{
		Definition: def,
		Lexer:      lx,
		Normalized: ng,
		Parser:     runtime.NewParser(lx, ng),
		Signature:  version.Signature(def),
		Version:    def.Version,
	}, nil
}
