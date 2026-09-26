package runtime

import (
	"github.com/guoyyit/parser-generator-go/lexer"
	"github.com/guoyyit/parser-generator-go/recovery"
)

// parseTokensRecover runs the parser in tolerant mode, carrying pre-existing
// lexical errors into the recovery pipeline.
func (p *Parser) parseTokensRecover(input string, toks []lexer.Token, opts Options, lexErrs []*lexer.Error) (*Result, error) {
	e := &engine{p: p, input: input, tokens: toks, memo: map[memoKey]*memoEntry{}}
	ans := e.applyRule(p.start, 0)
	res := &Result{Tree: nodeOf(ans)}
	if ans != nil && ans.node != nil && ans.node.End == len(toks) && len(lexErrs) == 0 {
		res.OK = true
		return res, nil
	}

	// Seed errors exclusively with lexical problems. Every parse-level error
	// is emitted by the recovery loop for the exact token it discards, so a
	// single fault can never be reported twice nor lost.
	var errs []*ParseError
	seen := map[int]bool{}
	for _, le := range lexErrs {
		if !seen[le.Pos] {
			seen[le.Pos] = true
			errs = append(errs, &ParseError{Pos: le.Pos, Line: le.Line, Col: le.Col, Message: le.Message})
		}
	}

	strategy := strategyOf(opts)
	tree, recoveredErrs := e.recoverWithSeeds(ans, strategy, seen, errs)
	res.Errors = recoveredErrs
	res.Tree = tree
	res.OK = tree != nil
	return res, nil
}

var _ = recovery.RecoverNone
