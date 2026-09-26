package runtime

import (
	"github.com/guoyyit/parser-generator-go/lexer"
	"github.com/guoyyit/parser-generator-go/recovery"
)

// recoverWithSeeds implements the configured recovery strategy starting from
// a parse answer and a pre-seeded set of errors (e.g. lexical errors).
//
// Strategy: "delete and retry". The token at the failure position is deleted
// (or, for SyncTo, tokens up to the next synchronization token are deleted)
// and parsing is retried on the remaining stream. Errors are deduplicated by
// original byte offset, so no error is reported twice and none is lost; the
// token stream shrinks on every iteration, guaranteeing termination.
// Error-free input never enters this path.
func (e *engine) recoverWithSeeds(first *answer, strategy recovery.Strategy, seen map[int]bool, seeds []*ParseError) (*Node, []*ParseError) {
	errs := seeds
	max := strategy.MaxErrors
	if max <= 0 {
		max = 100
	}
	var bestTree *Node
	cur := e.tokens
	// reached tracks the blocking position reported by the current attempt.
	reached := 0
	if first != nil && first.hasFur {
		reached = first.furthest
	} else if r := endOf(first); r > 0 {
		reached = r
	}

	deleteFrom := func(from int, toks []lexer.Token) []lexer.Token {
		if strategy.Mode == recovery.RecoverSyncTo {
			sync := e.syncSet(strategy)
			for j := from; j < len(toks); j++ {
				if sync[toks[j].Name] {
					return append(append([]lexer.Token{}, toks[:from]...), toks[j:]...)
				}
			}
		}
		return append(append([]lexer.Token{}, toks[:from]...), toks[from+1:]...)
	}

	for len(errs) < max {
		// The token blocking the parse is the first token beyond what the
		// current attempt managed to consume. The deepest-failure memo refines
		// this within the unconsumed suffix.
		failPos := reached
		if failPos >= len(cur) {
			// Reached end of input. Whether that is an error depends on
			// whether the final attempt consumed the whole remaining stream.
			complete := bestTree != nil && bestTree.End == len(cur)
			if !complete && !seen[eofKey] {
				seen[eofKey] = true
				line, col := e.eofLineCol(cur)
				errs = append(errs, &ParseError{
					Pos: e.inputLen(), Line: line, Col: col,
					Message: "unexpected end of input",
				})
			}
			break
		}
		bad := cur[failPos]
		next := deleteFrom(failPos, cur)
		if len(next) >= len(cur) {
			break
		}
		if !seen[bad.Pos] {
			seen[bad.Pos] = true
			errs = append(errs, parseErrFromToken(bad))
		}
		attempt := e.freshWith(next)
		ans := attempt.applyRule(e.p.start, 0)
		newReached := endOf(ans)
		if ans != nil && ans.hasFur {
			newReached = ans.furthest
		}
		if ans != nil && ans.node != nil {
			bestTree = ans.node
		}
		first = ans
		cur = next
		if newReached >= 0 {
			reached = newReached
		}
	}
	// If a prefix parsed but tokens remain unconsumed, report the first
	// leftover token once (deduplicated by offset).
	if bestTree != nil && bestTree.End < len(cur) {
		left := cur[bestTree.End]
		if !seen[left.Pos] {
			seen[left.Pos] = true
			errs = append(errs, parseErrFromToken(left))
		}
	}
	if bestTree == nil && first != nil {
		bestTree = first.node
	}
	return bestTree, errs
}

func parseErrFromToken(t lexer.Token) *ParseError {
	name := t.Name
	if name == lexer.ErrorTokenName {
		return &ParseError{Pos: t.Pos, Line: t.Line, Col: t.Col,
			Message: "unexpected character " + quote(t.Value)}
	}
	return &ParseError{Pos: t.Pos, Line: t.Line, Col: t.Col,
		Message: "unexpected token " + quote(t.Value) + " (" + name + ")"}
}

func (e *engine) syncSet(strategy recovery.Strategy) map[string]bool {
	out := map[string]bool{}
	for _, name := range strategy.FollowFallback {
		out[name] = true
	}
	for _, set := range e.p.followSets() {
		for t := range set {
			if t != "$EOF" {
				out[t] = true
			}
		}
	}
	return out
}

func (e *engine) freshWith(toks []lexer.Token) *engine {
	return &engine{p: e.p, input: e.input, tokens: toks, memo: map[memoKey]*memoEntry{}}
}

func quote(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		if r == '"' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	out = append(out, '"')
	return string(out)
}


// eofKey is a sentinel dedup key for a single end-of-input error.
const eofKey = -1

func (e *engine) inputLen() int { return len(e.input) }

func (e *engine) eofLineCol(toks []lexer.Token) (int, int) {
	if len(toks) > 0 {
		last := toks[len(toks)-1]
		return last.Line, last.Col + len([]rune(last.Value))
	}
	return lexer.LineCol(e.input, len(e.input))
}
