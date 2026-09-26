package runtime

import (
	"fmt"
	"sort"

	"github.com/guoyyit/parser-generator-go/lexer"
)

// answer is one memoized (rule, position) evaluation.
type answer struct {
	node     *Node   // primary (longest) result; nil denotes failure
	fail     string
	furthest int     // highest token position reached during this evaluation
	hasFur   bool
	altEnds  []altRes // every successful (end,node) for ordered-choice backtracking
}

type altRes struct {
	end  int
	node *Node
}

func mergeAnswers(a, b *answer) *answer {
	if a == nil {
		b.altEnds = append(b.altEnds, altRes{endOf(b), b.node})
		return b
	}
	if endOf(b) > endOf(a) {
		b.altEnds = append(b.altEnds, altRes{endOf(a), a.node})
		for _, x := range a.altEnds {
			if x.end != endOf(a) {
				b.altEnds = append(b.altEnds, x)
			}
		}
		b.furthest = a.furthest
		return b
	}
	a.altEnds = append(a.altEnds, altRes{endOf(b), b.node})
	for _, x := range b.altEnds {
		if x.end != endOf(b) {
			a.altEnds = append(a.altEnds, x)
		}
	}
	return a
}

// memoEntry caches one rule application at one token position.
type memoEntry struct {
	ans *answer // nil while the rule is currently being evaluated (left recursion)
	lr  *lrRec  // non-nil while this entry is a growing left recursion
}

// lrRec tracks one active left recursion, following Warth, Douglass &
// Millstein (Packrat Parsers that Support Left Recursion, 2008), Figure 5.
type lrRec struct {
	seed     *answer
	rule     string
	pos      int
	head     bool
	grown    bool
	involved map[*lrRec]bool
}

type engine struct {
	p      *Parser
	input  string
	tokens []lexer.Token // visible tokens only
	memo   map[memoKey]*memoEntry
	stack  []*lrRec
}

type memoKey struct {
	rule string
	pos  int // index into tokens
}

func (p *Parser) runEngine(input string, opts Options) (*Result, error) {
	strategy := strategyOf(opts)
	if strategy.Mode != 0 /* recovery.RecoverNone */ {
		all, lexErrs := p.lex.TokenizeTolerant(input)
		toks := lexer.SkipIgnored(all)
		return p.parseTokensRecover(input, toks, opts, lexErrs)
	}
	all, err := p.lex.Tokenize(input)
	if err != nil {
		le := err.(*lexer.Error)
		return &Result{
			Errors: []*ParseError{{Pos: le.Pos, Line: le.Line, Col: le.Col, Message: le.Message}},
		}, nil
	}
	toks := lexer.SkipIgnored(all)
	return p.parseTokens(input, toks, opts)
}

func (p *Parser) parseTokens(input string, toks []lexer.Token, opts Options) (*Result, error) {
	e := &engine{
		p:      p,
		input:  input,
		tokens: toks,
		memo:   map[memoKey]*memoEntry{},
	}
	ans := e.applyRule(p.start, 0)
	res := &Result{Tree: nodeOf(ans)}
	if ans != nil && ans.node != nil && ans.node.End == len(toks) {
		res.OK = true
		return res, nil
	}
	res.Errors = append(res.Errors, e.failureError(ans, len(toks)))
	res.OK = false
	return res, nil
}

func nodeOf(a *answer) *Node {
	if a == nil {
		return nil
	}
	return a.node
}

// ---- Warth growing-seed algorithm (Figure 5 of the 2008 paper) ----

func (e *engine) getMemo(key memoKey) *memoEntry {
	m, ok := e.memo[key]
	if !ok {
		m = &memoEntry{}
		e.memo[key] = m
	}
	return m
}

func (e *engine) onStack(lr *lrRec) bool {
	for _, x := range e.stack {
		if x == lr {
			return true
		}
	}
	return false
}

func (e *engine) push(lr *lrRec) { e.stack = append(e.stack, lr) }

func (e *engine) pop(lr *lrRec) {
	if n := len(e.stack); n > 0 && e.stack[n-1] == lr {
		e.stack = e.stack[:n-1]
	}
}

// applyRule corresponds to the paper's RuleApplicationCore + growLR +
// lazyRecursiveInvocation handling.
func (e *engine) applyRule(ruleName string, position int) *answer {
	m := e.getMemo(memoKey{ruleName, position})
	if m.lr != nil {
		// Recursive invocation while this entry is growing: this caller is
		// "involved" in the left recursion. Return a bare copy of the current
		// seed WITHOUT its alternative list: those alternatives belong to the
		// in-progress growth and would otherwise form a backtracking cycle.
		if len(e.stack) > 0 {
			e.stack[len(e.stack)-1].involved[m.lr] = true
		}
		seed := m.lr.seed
		return &answer{node: seed.node, furthest: seed.furthest, hasFur: seed.hasFur}
	}
	if m.ans != nil {
		return m.ans
	}

	// Enter a head LR frame and evaluate the rule body once.
	lr := &lrRec{
		rule:     ruleName,
		pos:      position,
		head:     true,
		involved: map[*lrRec]bool{},
	}
	m.lr = lr
	e.push(lr)
	var seed *answer
	bestFur := -1
	for _, alt := range e.p.rules[ruleName].Alts {
		if len(alt) > 0 && alt[0] == ruleName {
			continue // left-recursive alternative cannot seed
		}
		if node, end, ok := e.parseSeq(alt, position); ok {
			a := &answer{node: e.makeNode(ruleName, position, end, node), furthest: end, hasFur: true}
			seed = mergeAnswers(seed, a)
		} else if end > bestFur {
			bestFur = end
		}
	}
	lr.seed = seed
	if seed == nil {
		// No seed: the rule does not match here.
		e.pop(lr)
		m.lr = nil
		m.ans = &answer{fail: ruleName, furthest: bestFur, hasFur: bestFur >= 0}
		return m.ans
	}
	// The LR frame stays on the stack throughout growth. growLR pops it and
	// clears m.lr when finished.
	m.ans = seed
	e.growLR(lr, m)
	return m.ans
}

// growLR repeatedly re-evaluates left-recursive alternatives with the current
// seed until no alternative extends the parse, then invalidates (lazily
// re-evaluates) entries involved in the recursion.
func (e *engine) growLR(lr *lrRec, m *memoEntry) {
	if lr.seed == nil || lr.grown {
		return
	}
	lr.grown = true
	for {
		m.ans = lr.seed
		var grew *answer
		growFur := -1
		for _, alt := range e.p.rules[lr.rule].Alts {
			if len(alt) == 0 || alt[0] != lr.rule {
				continue
			}
					node, end, ok := e.parseSeq(alt, lr.pos)
			if ok && end > endOf(lr.seed) {
				grew = &answer{node: e.makeNode(lr.rule, lr.pos, end, node), furthest: end, hasFur: true}
				break
			} else if !ok && end > growFur {
				growFur = end
			}
		}
		if grew == nil && growFur > endOf(lr.seed) {
			lr.seed.furthest = growFur
			lr.seed.hasFur = true
		}
		if grew == nil {
			break
		}
		lr.seed = grew
	}
	e.pop(lr)
	m.lr = nil
	if lr.head {
		// Invalidate dependent memo entries that were evaluated against a
		// shorter seed. Lazy re-evaluation grows them once more.
		for involved := range lr.involved {
			if !e.onStack(involved) {
				e.lazyEval(involved)
			}
		}
	}
}

func (e *engine) lazyEval(lr *lrRec) {
	m := e.getMemo(memoKey{lr.rule, lr.pos})
	m.ans = lr.seed
	lr.head = false
	e.push(lr)
	e.growLR(lr, m)
}

func endOf(a *answer) int {
	if a == nil || a.node == nil {
		return -1
	}
	return a.node.End
}

// ---- sequences ----

func (e *engine) parseSeq(seq []string, pos int) ([]*Node, int, bool) {
	// DFS with backtracking over the alternative results of non-terminals.
	// The primary (longest) result is tried first, giving repetition sugar
	// greedy semantics; shorter results are tried if the suffix then matches.
	type frame struct {
		symIdx int
		cur    int
		nodes  []*Node
		ends   []altRes
		pick   int
		guard  [2]string
		guarded bool
	}
	stack := []frame{{symIdx: 0, cur: pos}}
	var furthest int
	steps := 0
	maxSteps := 200000
	// Active non-terminal evaluations on this DFS path, used to prune cycles
	// that consume no input (e.g. star's epsilon branch revisited).
	active := map[[2]string]bool{}

	for len(stack) > 0 {
		steps++
		if steps > maxSteps {
			panic("parseSeq steps exceeded for seq ending " + itoa2(len(seq)) + " frames=" + itoa2(len(stack)))
		}
		top := &stack[len(stack)-1]
		if top.symIdx >= len(seq) {
			return top.nodes, top.cur, true
		}
		sym := seq[top.symIdx]

		if e.isTerminal(sym) {
			if top.cur < len(e.tokens) && e.tokens[top.cur].Name == sym {
				nodes := append(cp(top.nodes), e.terminalNode(sym, top.cur))
				stack = append(stack, frame{symIdx: top.symIdx + 1, cur: top.cur + 1, nodes: nodes})
				continue
			}
			if top.cur > furthest {
				furthest = top.cur
			}
			stack = stack[:len(stack)-1]
			continue
		}

		if top.ends == nil {
			key := [2]string{sym, itoa2(top.cur)}
			if active[key] {
				stack = stack[:len(stack)-1]
				continue
			}
			active[key] = true
			top.guard = key
			top.guarded = true
			// First visit: evaluate the symbol and collect its possible ends.
			a := e.applyRule(sym, top.cur)
			if a == nil || a.node == nil {
				f := top.cur
				if a != nil && a.hasFur && a.furthest > f {
					f = a.furthest
				}
				if f > furthest {
					furthest = f
				}
				// Mark exhausted so the frame is popped on the next visit
				// instead of re-evaluating forever.
				top.ends = []altRes{}
				continue
			}
			ends := append([]altRes{{end: a.node.End, node: a.node}}, a.altEnds...)
			ends = dedupEnds(ends)
			top.ends = ends
			top.pick = 0
		}
		if top.pick >= len(top.ends) {
			if top.guarded {
				delete(active, top.guard)
			}
			stack = stack[:len(stack)-1]
			continue
		}
		choice := top.ends[top.pick]
		top.pick++
		nodes := append(cp(top.nodes), choice.node)
		stack = append(stack, frame{symIdx: top.symIdx + 1, cur: choice.end, nodes: nodes})
	}
	return nil, furthest, false
}

func cp(nodes []*Node) []*Node {
	out := make([]*Node, len(nodes))
	copy(out, nodes)
	return out
}

func dedupEnds(in []altRes) []altRes {
	seen := map[int]bool{}
	out := in[:0]
	for _, x := range in {
		if seen[x.end] {
			continue
		}
		seen[x.end] = true
		out = append(out, x)
	}
	return out
}

func (e *engine) isTerminal(sym string) bool {
	_, isRule := e.p.rules[sym]
	return !isRule
}

func (e *engine) terminalNode(name string, tokIdx int) *Node {
	t := e.tokens[tokIdx]
	return &Node{
		Name:     name,
		Terminal: true,
		Token:    &t,
		Start:    tokIdx,
		End:      tokIdx + 1,
		Line:     t.Line,
		Col:      t.Col,
	}
}

// makeNode builds a tree node. Synthetic wrapper rules are transparent: a
// synthetic node with a single child is replaced by that child.
func (e *engine) makeNode(rule string, start, end int, children []*Node) *Node {
	if e.p.synthetic[rule] {
		if len(children) == 1 {
			return children[0]
		}
	}
	n := &Node{Name: rule, Start: start, End: end, Children: children}
	if start < len(e.tokens) {
		n.Line = e.tokens[start].Line
		n.Col = e.tokens[start].Col
	} else if len(e.tokens) > 0 {
		last := e.tokens[len(e.tokens)-1]
		n.Line = last.Line
		n.Col = last.Col
	}
	return n
}

// ---- errors ----

func (e *engine) failureError(a *answer, fallbackPos int) *ParseError {
	pos := e.deepestFail()
	if pos < 0 {
		pos = fallbackPos
	}
	var msg string
	if pos >= len(e.tokens) {
		msg = "unexpected end of input"
	} else {
		t := e.tokens[pos]
		msg = fmt.Sprintf("unexpected token %q (%s)", t.Value, t.Name)
	}
	line, col := e.posLineCol(pos)
	return &ParseError{Pos: pos, Line: line, Col: col, Message: msg}
}

// failureErrorByte is failureError with Pos as a byte offset in the input.
func (e *engine) failureErrorByte(a *answer, fallbackPos int) *ParseError {
	pe := e.failureError(a, fallbackPos)
	pe.Pos = e.byteOffset(pe.Pos)
	return pe
}

func (e *engine) byteOffset(tokIdx int) int {
	if tokIdx >= 0 && tokIdx < len(e.tokens) {
		return e.tokens[tokIdx].Pos
	}
	return len(e.input)
}

// deepestFail finds the highest token position at which a rule application
// failed — the most useful error location.
func (e *engine) deepestFail() int {
	var keys []memoKey
	for k, m := range e.memo {
		if m.ans != nil && m.ans.fail != "" && (len(keys) == 0 || k.pos > keys[0].pos) {
			keys = append(keys[:0], k)
		}
	}
	_ = sort.IntSlice{}
	if len(keys) > 0 {
		return keys[0].pos
	}
	return -1
}

func (e *engine) posLineCol(tokIdx int) (int, int) {
	if tokIdx < len(e.tokens) {
		t := e.tokens[tokIdx]
		return t.Line, t.Col
	}
	if len(e.tokens) > 0 {
		last := e.tokens[len(e.tokens)-1]
		return last.Line, last.Col + len([]rune(last.Value))
	}
	return lexer.LineCol(e.input, len(e.input))
}

func itoa2(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
