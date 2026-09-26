// Package runtime executes a generated parser: a memoized packrat parser with
// Warth-style growing-seed left recursion (direct and indirect), precise
// line/column errors and configurable recovery.
//
// A Parser holds only immutable compiled data; every Parse invocation works
// on local state and is therefore safe to run concurrently.
package runtime

import (
	"github.com/guoyyit/parser-generator-go/analysis"
	"github.com/guoyyit/parser-generator-go/lexer"
	"github.com/guoyyit/parser-generator-go/recovery"
)

// Node is a node of the concrete parse tree.
type Node struct {
	Name     string
	Terminal bool
	Token    *lexer.Token
	Children []*Node
	Start    int // byte offset in the original input
	End      int
	Line     int // 1-based, of Start
	Col      int
}

// ParseError is a single located parse error.
type ParseError struct {
	Pos     int
	Line    int
	Col     int
	Message string
}

func (e *ParseError) Error() string { return e.Message }

// Result is the structured outcome of a parse.
type Result struct {
	Tree   *Node
	Errors []*ParseError
	OK     bool
}

// Parser is an immutable, concurrently reusable parser instance.
type Parser struct {
	rules     map[string]*analysis.NormalRule
	order     []string
	lex       *lexer.Compiled
	start     string
	nullable  map[string]bool
	synthetic map[string]bool
	follow    map[string]map[string]bool
}

// NewParser assembles a parser from immutable compiled artifacts. It performs
// no per-input work.
func NewParser(lex *lexer.Compiled, ng *analysis.NormalGrammar) *Parser {
	p := &Parser{
		rules:     ng.Rules,
		order:     ng.Order,
		lex:       lex,
		start:     ng.Start,
		nullable:  ng.ComputeNullable(),
		synthetic: map[string]bool{},
		follow:    ng.ComputeFollow(),
	}
	for name, r := range ng.Rules {
		p.synthetic[name] = r.Synthetic
	}
	return p
}

// Options configures one parse invocation; state never escapes the call.
type Options struct {
	Recovery recovery.Strategy
}

func strategyOf(o Options) recovery.Strategy { return o.Recovery }

// FollowSets exposes the computed FOLLOW sets (used for recovery).
func (p *Parser) FollowSets() map[string]map[string]bool { return p.follow }

func (p *Parser) followSets() map[string]map[string]bool { return p.follow }

// Parse parses input without mutating any shared parser state.
func (p *Parser) Parse(input string) (*Result, error) {
	return p.ParseWith(input, Options{Recovery: recovery.DefaultStrategy()})
}

// ParseWith parses input with per-call options.
func (p *Parser) ParseWith(input string, opts Options) (*Result, error) {
	return p.runEngine(input, opts)
}
