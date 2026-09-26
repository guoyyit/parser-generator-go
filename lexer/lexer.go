// Package lexer implements longest-match tokenization with priority
// tie-breaking and ignorable tokens (whitespace etc.).
package lexer

import (
	"fmt"
	"regexp"
	"sort"
	"unicode/utf8"

	"github.com/guoyyit/parser-generator-go/grammar"
)

// Token is a single lexical unit located in the original input.
type Token struct {
	Name    string
	Value   string
	Pos     int
	End     int
	Line    int // 1-based
	Col     int // 1-based, rune-based
	Ignored bool
	Index   int // stable index among visible tokens, preserved under recovery deletion
}

// Error describes a lexical failure with line/column location.
type Error struct {
	Pos     int
	Line    int
	Col     int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message)
}

type compiledRule struct {
	rule grammar.TokenRule
	re   *regexp.Regexp
}

// Compiled is an immutable, concurrency-safe compiled lexer. A Compiled value
// holds no per-input state, so Tokenize may run concurrently.
type Compiled struct {
	rules []compiledRule
}

// Compile compiles lexical rules. Duplicate declarations and unresolvable
// conflicts (identical patterns with identical priority) are rejected
// explicitly: ambiguous tokenization is never resolved silently.
func Compile(tokens []grammar.TokenRule) (*Compiled, error) {
	if len(tokens) == 0 {
		return nil, fmt.Errorf("lexer: at least one token rule is required")
	}
	seenName := map[string]bool{}
	type sig struct{ pattern string; priority int }
	sigOwners := map[sig]string{}
	c := &Compiled{}
	for _, t := range tokens {
		if seenName[t.Name] {
			return nil, fmt.Errorf("lexer: duplicate token rule %q", t.Name)
		}
		seenName[t.Name] = true
		re, err := regexp.Compile(`\A(?:` + t.Pattern + `)`)
		if err != nil {
			return nil, fmt.Errorf("lexer: token rule %q: %w", t.Name, err)
		}
		s := sig{t.Pattern, t.Priority}
		if owner, ok := sigOwners[s]; ok {
			return nil, fmt.Errorf(
				"lexer: token rules %q and %q have identical pattern %q and priority %d; "+
					"declare distinct priorities or remove the duplicate",
				owner, t.Name, t.Pattern, t.Priority)
		}
		sigOwners[s] = t.Name
		c.rules = append(c.rules, compiledRule{rule: t, re: re})
	}
	return c, nil
}

// lineCol computes the 1-based line and rune column of a byte offset.
func lineCol(input string, off int) (int, int) {
	line, col := 1, 1
	for i := 0; i < off && i < len(input); {
		r, size := utf8.DecodeRuneInString(input[i:])
		if r == '\n' {
			line++
			col = 1
		} else {
			col++
		}
		i += size
	}
	return line, col
}

// LineCol exposes line/column computation for the runtime package.
func LineCol(input string, off int) (int, int) { return lineCol(input, off) }

// match represents one rule's prefix match at a position.
type match struct {
	idx    int // index in c.rules
	length int
}

// candidates returns all rules matching at off, ordered longest first and
// then highest priority first.
func (c *Compiled) candidates(input string, off int) []match {
	rest := input[off:]
	var ms []match
	for i, cr := range c.rules {
		loc := cr.re.FindStringIndex(rest)
		if loc == nil {
			continue
		}
		if loc[0] != 0 {
			continue // patterns are anchored; this should not happen
		}
		if loc[1] == 0 {
			continue // empty matches are rejected at grammar validation
		}
		ms = append(ms, match{idx: i, length: loc[1]})
	}
	sort.Slice(ms, func(a, b int) bool {
		if ms[a].length != ms[b].length {
			return ms[a].length > ms[b].length
		}
		pa := c.rules[ms[a].idx].rule.Priority
		pb := c.rules[ms[b].idx].rule.Priority
		if pa != pb {
			return pa > pb
		}
		return false // equal length and priority: callers must flag the tie
	})
	return ms
}

// Tokenize runs the lexer over input. Ignored rules are matched like any
// other but the resulting tokens are marked and filtered by SkipIgnored.
func (c *Compiled) Tokenize(input string) ([]Token, error) {
	var toks []Token
	off := 0
	for off < len(input) {
		ms := c.candidates(input, off)
		if len(ms) == 0 {
			line, col := lineCol(input, off)
			return nil, &Error{
				Pos:     off,
				Line:    line,
				Col:     col,
				Message: fmt.Sprintf("unexpected character %q", nextRune(input, off)),
			}
		}
		best := ms[0]
		// Same length and same priority: ambiguous tokenization.
		if len(ms) > 1 {
			b := ms[1]
			if b.length == best.length &&
				c.rules[b.idx].rule.Priority == c.rules[best.idx].rule.Priority {
				line, col := lineCol(input, off)
				return nil, &Error{
					Pos: off, Line: line, Col: col,
					Message: fmt.Sprintf("lexical ambiguity at %d:%d: rules %q and %q both match %q with equal priority; declare a priority",
						line, col,
						c.rules[best.idx].rule.Name,
						c.rules[b.idx].rule.Name,
						input[off:off+best.length]),
				}
			}
		}
		cr := c.rules[best.idx]
		line, col := lineCol(input, off)
		toks = append(toks, Token{
			Name:    cr.rule.Name,
			Value:   input[off : off+best.length],
			Pos:     off,
			End:     off + best.length,
			Line:    line,
			Col:     col,
			Ignored: cr.rule.Ignore,
		})
		off += best.length
	}
	return toks, nil
}

// SkipIgnored returns only parser-visible tokens.
func SkipIgnored(toks []Token) []Token {
	out := toks[:0:0]
	idx := 0
	for _, t := range toks {
		if !t.Ignored {
			t.Index = idx
			idx++
			out = append(out, t)
		}
	}
	return out
}

func nextRune(s string, off int) string {
	if off >= len(s) {
		return ""
	}
	r := []rune(s[off:])
	return string(r[0])
}

// ErrorTokenName is the synthetic token name emitted for a byte/rune that no
// rule matches during tolerant tokenization.
const ErrorTokenName = "$ERROR"

// TokenizeTolerant tokenizes input, emitting a synthetic ErrorTokenName token
// for an unmatched rune instead of aborting. It always returns one
// *Error per encountered bad rune, each at a distinct original offset.
func (c *Compiled) TokenizeTolerant(input string) ([]Token, []*Error) {
	var toks []Token
	var errs []*Error
	off := 0
	for off < len(input) {
		ms := c.candidates(input, off)
		if len(ms) == 0 {
			line, col := lineCol(input, off)
			r := []rune(input[off:])[0]
			width := len(string(r))
			errs = append(errs, &Error{Pos: off, Line: line, Col: col,
				Message: fmt.Sprintf("unexpected character %q", string(r))})
			toks = append(toks, Token{
				Name: ErrorTokenName, Value: string(r),
				Pos: off, End: off + width, Line: line, Col: col,
			})
			off += width
			continue
		}
		best := ms[0]
		if len(ms) > 1 {
			b := ms[1]
			if b.length == best.length &&
				c.rules[b.idx].rule.Priority == c.rules[best.idx].rule.Priority {
				line, col := lineCol(input, off)
				errs = append(errs, &Error{Pos: off, Line: line, Col: col,
					Message: fmt.Sprintf("lexical ambiguity: rules %q and %q both match %q with equal priority",
						c.rules[best.idx].rule.Name, c.rules[b.idx].rule.Name,
						input[off:off+best.length])})
				// skip the ambiguous run to continue
				off += best.length
				continue
			}
		}
		cr := c.rules[best.idx]
		line, col := lineCol(input, off)
		toks = append(toks, Token{
			Name: cr.rule.Name, Value: input[off : off+best.length],
			Pos: off, End: off + best.length, Line: line, Col: col,
			Ignored: cr.rule.Ignore,
		})
		off += best.length
	}
	return toks, errs
}
