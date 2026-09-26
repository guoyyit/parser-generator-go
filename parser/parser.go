package parser

import (
	"fmt"
	"sort"

	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/lexer"
)

// Parser 是由 generator 生成（或直接构造）的可复用解析器。
// Parser 持有的全部状态在构造后只读，Parse 可并发调用。
type Parser struct {
	g     *grammar.Grammar
	lx    *lexer.Lexer
	start string
	rules map[string]*grammar.Rule
}

// New 构造运行时解析器。g 会被深拷贝，之后调用方对 g 的修改不影响解析器。
// 这里只做结构性检查（规则引用可解析），冲突检测由 generator 完成。
func New(g *grammar.Grammar, lx *lexer.Lexer, start string) (*Parser, error) {
	if g == nil {
		return nil, fmt.Errorf("parser: nil grammar")
	}
	return NewShared(g.Clone(), lx, start)
}

// NewShared 与 New 相同，但不拷贝语法定义：调用方必须保证
// 在解析器生命周期内不修改 g（增量校验器等内部复用场景）。
func NewShared(g *grammar.Grammar, lx *lexer.Lexer, start string) (*Parser, error) {
	if g == nil {
		return nil, fmt.Errorf("parser: nil grammar")
	}
	if lx == nil {
		return nil, fmt.Errorf("parser: nil lexer")
	}
	cp := g
	if start == "" {
		start = cp.Start
	}
	if start == "" {
		return nil, fmt.Errorf("parser: no start rule specified")
	}
	rules := make(map[string]*grammar.Rule, len(cp.Rules))
	for i := range cp.Rules {
		rules[cp.Rules[i].Name] = &cp.Rules[i]
	}
	if _, ok := rules[start]; !ok {
		return nil, fmt.Errorf("parser: start rule %q not defined", start)
	}
	// 结构检查：所有非终结符引用必须可解析，所有终结符引用必须有对应词法规则。
	tokens := make(map[string]bool, len(cp.LexerRules))
	for _, lr := range cp.LexerRules {
		tokens[lr.Name] = true
	}
	for i := range cp.Rules {
		r := &cp.Rules[i]
		for _, alt := range r.Alternatives {
			if err := checkAlt(rules, tokens, r.Name, alt); err != nil {
				return nil, err
			}
		}
	}
	return &Parser{g: cp, lx: lx, start: start, rules: rules}, nil
}

func checkAlt(rules map[string]*grammar.Rule, tokens map[string]bool, rule string, a grammar.Alternative) error {
	for _, t := range a.Terms {
		switch t.Kind {
		case grammar.TermNonTerminal:
			if _, ok := rules[t.Name]; !ok {
				return fmt.Errorf("parser: rule %q references undefined rule %q", rule, t.Name)
			}
		case grammar.TermTerminal:
			if !tokens[t.Name] {
				return fmt.Errorf("parser: rule %q references undefined token %q", rule, t.Name)
			}
		case grammar.TermOptional, grammar.TermZeroOrMore, grammar.TermOneOrMore, grammar.TermGroup:
			if t.Sub == nil {
				return fmt.Errorf("parser: rule %q has nil sub-expression", rule)
			}
			if err := checkAlt(rules, tokens, rule, *t.Sub); err != nil {
				return err
			}
		}
	}
	return nil
}

// Start 返回起始规则名。
func (p *Parser) Start() string { return p.start }

// Grammar 返回解析器持有的语法定义的深拷贝。
func (p *Parser) Grammar() *grammar.Grammar { return p.g.Clone() }

// Lexer 返回解析器使用的词法器（只读，可安全共享）。
func (p *Parser) Lexer() *lexer.Lexer { return p.lx }

// Parse 解析输入。cfg 可为 nil（等价于默认配置：快速失败）。
// 每次调用使用独立的会话状态，可并发执行。
func (p *Parser) Parse(input string, cfg *Config) *Result {
	c := Config{}
	if cfg != nil {
		c = *cfg
	}
	s := newSession(p, input, &c)
	return s.run()
}

// ---------------------------------------------------------------------------
// 会话：单次 Parse 调用的全部可变状态。
// ---------------------------------------------------------------------------

type reply struct {
	node *Node
	end  int // token 游标（不含）
}

type memoKey struct {
	rule string
	pos  int
}

type memoEntry struct {
	res *reply   // 具体结果；nil 表示失败
	lr  *lrFrame // 非 nil 表示左递归占位
	// failExpects 记录求值失败时的期望条目；命中缓存失败时回放，
	// 使错误定位不依赖当次回溯（恢复分段解析因此能得到正确位置）。
	failExpects []expectEntry
}

type lrFrame struct {
	rule string
	seed *reply
	head *head
}

type head struct {
	rule     string
	involved map[string]bool
	eval     map[string]bool
}

type expectEntry struct {
	pos  int
	name string
}

type session struct {
	p     *Parser
	input string
	cfg   *Config

	ts    *tokenStream
	memo  map[memoKey]*memoEntry
	stack []*lrFrame

	expectations []expectEntry
	lines        *lineIndex

	errs       []*Error
	errKeys    map[string]bool
	lexOffsets map[int]bool
}

func newSession(p *Parser, input string, cfg *Config) *session {
	s := &session{
		p:          p,
		input:      input,
		cfg:        cfg,
		memo:       map[memoKey]*memoEntry{},
		lines:      newLineIndex(input),
		errKeys:    map[string]bool{},
		lexOffsets: map[int]bool{},
	}
	s.ts = &tokenStream{lx: p.lx, input: input, truncated: -1, s: s}
	return s
}

func (s *session) recovering() bool { return s.cfg.Recovery != RecoveryFailFast }

// run 执行解析并按配置进行错误恢复。
func (s *session) run() *Result {
	res := s.applyRule(s.p.start, 0)
	total := s.ts.forceAll() // 填满 token 流，暴露尾部词法错误

	if res != nil && res.end == total && len(s.errs) == 0 {
		return &Result{Root: res.node, Consumed: len(s.input)}
	}

	if !s.recovering() {
		s.commitParseError(total, 0)
		s.dedupeParseAtLexErrors()
		// 快速失败不返回部分树：调用方拿 Root==nil 即知解析未成功。
		return &Result{Root: nil, Errors: s.errs, Consumed: consumedBytes(s, res)}
	}

	// 恢复模式：分段解析，逐段报告错误，跳过失败区域后继续。
	// commitParseError 提交后会清空期望列表，因此各段互不污染。
	root := &Node{Symbol: s.p.start, Offset: 0}
	pos := 0
	if res != nil && res.end > 0 {
		root.Children = append(root.Children, res.node)
		pos = res.end
	}
	if res == nil || res.end < total {
		s.commitParseError(total, pos)
	}
	for pos < total && !s.errorLimitReached() {
		pos = s.skip(pos, total)
		if pos >= total {
			break
		}
		r := s.applyRule(s.p.start, pos)
		if r != nil && r.end > pos {
			root.Children = append(root.Children, r.node)
			pos = r.end
			if pos < total {
				s.addError(pos, []string{"end of input"})
			}
			continue
		}
		s.commitParseError(total, pos)
	}
	s.dedupeParseAtLexErrors()
	root.End = consumedBytes(s, &reply{end: pos})
	return &Result{Root: root, Errors: s.errs, Consumed: root.End}
}

// dedupeParseAtLexErrors 移除与词法错误同位置的语法错误（词法错误为根因）。
func (s *session) dedupeParseAtLexErrors() {
	if len(s.lexOffsets) == 0 {
		return
	}
	kept := s.errs[:0]
	for _, e := range s.errs {
		if e.FromLexer || !s.lexOffsets[e.Offset] {
			kept = append(kept, e)
		}
	}
	s.errs = kept
}

func rootOrNil(res *reply) *Node {
	if res == nil {
		return nil
	}
	return res.node
}

func consumedBytes(s *session, res *reply) int {
	if res == nil {
		return 0
	}
	return s.byteOffset(res.end)
}

// commitParseError 把当前最远期望位置固化为一条解析错误（去重后追加）。
// fallbackPos 是没有任何期望记录时的报告位置（如恢复分段起点）。
func (s *session) commitParseError(total, fallbackPos int) {
	if len(s.expectations) == 0 {
		s.addError(fallbackPos, nil)
		s.expectations = s.expectations[:0]
		return
	}
	maxPos := -1
	for _, e := range s.expectations {
		if e.pos > maxPos {
			maxPos = e.pos
		}
	}
	// 若期望只停在词法截断处（快速失败词法错误导致），
	// 不再追加派生的"期望输入结束/终结符"错误，词法错误才是根因。
	if s.ts.truncated >= 0 && maxPos >= len(s.ts.toks) {
		s.expectations = s.expectations[:0]
		return
	}
	set := map[string]bool{}
	for _, e := range s.expectations {
		if e.pos == maxPos {
			set[e.name] = true
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	s.addError(maxPos, names)
	// 已提交，清空以便后续段落重新收集。
	s.expectations = s.expectations[:0]
}

// addError 在 token 游标 pos 处记录一条错误；按 (偏移, 内容) 去重。
func (s *session) addError(pos int, expected []string) {
	if s.errorLimitReached() {
		return
	}
	offset := s.byteOffset(pos)
	e := &Error{Offset: offset, Expected: expected}
	e.Line, e.Column = s.lines.locate(offset)
	tok, ok := s.ts.get(pos)
	if ok {
		e.Found = fmt.Sprintf("%s (%q)", tok.Type, tok.Value)
	} else {
		e.Found = "end of input"
	}
	if len(expected) > 0 {
		e.Msg = fmt.Sprintf("expected %s, found %s", joinOr(expected), e.Found)
	} else {
		e.Msg = fmt.Sprintf("unexpected %s", e.Found)
	}
	key := fmt.Sprintf("%d|%s", offset, e.Msg)
	if s.errKeys[key] {
		return
	}
	s.errKeys[key] = true
	s.errs = append(s.errs, e)
}

// addLexError 记录词法错误；按偏移去重，保证不漏报不重复。
func (s *session) addLexError(offset int, msg string) {
	key := fmt.Sprintf("lex|%d", offset)
	if s.errKeys[key] {
		return
	}
	s.errKeys[key] = true
	s.lexOffsets[offset] = true
	e := &Error{Offset: offset, Msg: msg, Found: "invalid input", FromLexer: true}
	e.Line, e.Column = s.lines.locate(offset)
	s.errs = append(s.errs, e)
}

func (s *session) errorLimitReached() bool {
	return s.cfg.MaxErrors > 0 && len(s.errs) >= s.cfg.MaxErrors
}

// skip 按恢复策略从 pos 跳过 token，返回新的游标（保证 > pos 除非已到末尾）。
func (s *session) skip(pos, total int) int {
	switch s.cfg.Recovery {
	case RecoverySkipToken:
		return pos + 1
	case RecoverySync:
		for i := pos; i < total; i++ {
			tok, ok := s.ts.get(i)
			if !ok {
				return total
			}
			if s.cfg.SyncTokens[tok.Type] {
				if i == pos {
					return pos + 1 // 同步词就在当前位置仍失败，强制前进
				}
				return i
			}
		}
		return total
	default:
		return pos + 1
	}
}

// ---------------------------------------------------------------------------
// 左递归 packrat 求值（Warth et al. 2008）。
// ---------------------------------------------------------------------------

func (s *session) applyRule(rule string, pos int) *reply {
	key := memoKey{rule: rule, pos: pos}
	if m, ok := s.memo[key]; ok {
		if m.lr != nil {
			s.setupLR(rule, m.lr)
			if m.lr.head != nil && m.lr.head.eval[rule] {
				// 生长期：重新求值并保留最长结果。
				r := s.evalBody(rule, pos)
				if longer(r, m.lr.seed) {
					m.lr.seed = r
				}
				return m.lr.seed
			}
			if m.lr.seed == nil {
				s.expectations = append(s.expectations, m.failExpects...)
			}
			return m.lr.seed
		}
		if m.res == nil {
			s.expectations = append(s.expectations, m.failExpects...)
		}
		return m.res
	}
	frame := &lrFrame{rule: rule}
	s.stack = append(s.stack, frame)
	m := &memoEntry{lr: frame}
	s.memo[key] = m
	snap := len(s.expectations)
	ans := s.evalBody(rule, pos)
	s.stack = s.stack[:len(s.stack)-1]
	if frame.head != nil {
		frame.seed = ans
		res := s.lrAnswer(rule, pos, m)
		if res == nil {
			m.failExpects = append([]expectEntry(nil), s.expectations[snap:]...)
		}
		return res
	}
	m.lr = nil
	m.res = ans
	if ans == nil {
		m.failExpects = append([]expectEntry(nil), s.expectations[snap:]...)
	}
	return ans
}

func (s *session) setupLR(rule string, l *lrFrame) {
	if l.head == nil {
		l.head = &head{rule: rule, involved: map[string]bool{}, eval: map[string]bool{}}
	}
	for i := len(s.stack) - 1; i >= 0; i-- {
		f := s.stack[i]
		if f == l {
			break
		}
		f.head = l.head
		l.head.involved[f.rule] = true
	}
}

func (s *session) lrAnswer(rule string, pos int, m *memoEntry) *reply {
	h := m.lr.head
	seed := m.lr.seed
	if h.rule != rule {
		return seed
	}
	m.lr = nil
	m.res = seed
	if seed == nil {
		return nil
	}
	return s.growLR(h, rule, pos, m)
}

func (s *session) growLR(h *head, rule string, pos int, m *memoEntry) *reply {
	h.eval = copySet(h.involved)
	for {
		ans := s.evalBody(rule, pos)
		if ans == nil || ans.end <= m.res.end {
			break
		}
		m.res = ans
	}
	// 生长结束后清空 eval 集，后续应用直接使用最优种子。
	h.eval = map[string]bool{}
	return m.res
}

func longer(a, b *reply) bool {
	if b == nil {
		return a != nil
	}
	return a != nil && a.end > b.end
}

func copySet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k := range in {
		out[k] = true
	}
	return out
}

// evalBody 按声明顺序尝试规则的各候选分支（生成期已排除分支间歧义）。
func (s *session) evalBody(rule string, pos int) *reply {
	r := s.p.rules[rule]
	for i := range r.Alternatives {
		nodes, end, ok := s.evalSeq(r.Alternatives[i].Terms, pos)
		if ok {
			n := &Node{
				Symbol:   rule,
				Children: nodes,
				Offset:   s.byteOffset(pos),
				End:      s.byteOffset(end),
			}
			return &reply{node: n, end: end}
		}
	}
	return nil
}

// evalSeq 顺序求值一个分支，返回子节点序列与结束游标。
func (s *session) evalSeq(terms []grammar.Term, pos int) ([]*Node, int, bool) {
	var nodes []*Node
	cur := pos
	for i := range terms {
		ns, next, ok := s.evalTerm(&terms[i], cur)
		if !ok {
			return nil, pos, false
		}
		nodes = append(nodes, ns...)
		cur = next
	}
	return nodes, cur, true
}

func (s *session) evalTerm(t *grammar.Term, pos int) ([]*Node, int, bool) {
	switch t.Kind {
	case grammar.TermTerminal:
		tok, ok := s.ts.get(pos)
		if ok && tok.Type == t.Name {
			return []*Node{{
				Symbol: t.Name,
				Value:  tok.Value,
				Offset: tok.Offset,
				End:    tok.Offset + len(tok.Value),
			}}, pos + 1, true
		}
		s.expect(pos, t.Name)
		return nil, pos, false

	case grammar.TermNonTerminal:
		r := s.applyRule(t.Name, pos)
		if r == nil {
			return nil, pos, false
		}
		return []*Node{r.node}, r.end, true

	case grammar.TermOptional:
		snap := s.markExpect()
		nodes, end, ok := s.evalSeq(t.Sub.Terms, pos)
		if !ok {
			s.restoreExpect(snap) // 可选项失败是正常回溯，不产生期望噪声
			return nil, pos, true
		}
		return []*Node{{Symbol: "?", Children: nodes, Offset: s.byteOffset(pos), End: s.byteOffset(end)}}, end, true

	case grammar.TermZeroOrMore:
		var children []*Node
		cur := pos
		for {
			nodes, end, ok := s.evalSeq(t.Sub.Terms, cur)
			if !ok || end == cur { // 空匹配保护，避免死循环
				break
			}
			children = append(children, nodes...)
			cur = end
		}
		return []*Node{{Symbol: "*", Children: children, Offset: s.byteOffset(pos), End: s.byteOffset(cur)}}, cur, true

	case grammar.TermOneOrMore:
		nodes, end, ok := s.evalSeq(t.Sub.Terms, pos)
		if !ok {
			return nil, pos, false
		}
		children := append([]*Node{}, nodes...)
		cur := end
		for {
			nodes, end, ok := s.evalSeq(t.Sub.Terms, cur)
			if !ok || end == cur {
				break
			}
			children = append(children, nodes...)
			cur = end
		}
		return []*Node{{Symbol: "+", Children: children, Offset: s.byteOffset(pos), End: s.byteOffset(cur)}}, cur, true

	case grammar.TermGroup:
		nodes, end, ok := s.evalSeq(t.Sub.Terms, pos)
		if !ok {
			return nil, pos, false
		}
		return []*Node{{Symbol: "()", Children: nodes, Offset: s.byteOffset(pos), End: s.byteOffset(end)}}, end, true
	}
	return nil, pos, false
}

// ---------------------------------------------------------------------------
// 期望集（用于错误消息）：仅追加，回溯时截断，无错误输入上只有廉价 append。
// ---------------------------------------------------------------------------

func (s *session) expect(pos int, name string) {
	s.expectations = append(s.expectations, expectEntry{pos: pos, name: name})
}

func (s *session) markExpect() int { return len(s.expectations) }

func (s *session) restoreExpect(mark int) { s.expectations = s.expectations[:mark] }

// byteOffset 把 token 游标换算为字节偏移；会按需驱动词法流。
func (s *session) byteOffset(pos int) int {
	tok, ok := s.ts.get(pos)
	if !ok {
		return len(s.input)
	}
	return tok.Offset
}

func joinOr(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	out := ""
	for i, n := range names {
		if i == len(names)-1 {
			out += " or " + n
		} else if i == 0 {
			out = n
		} else {
			out += ", " + n
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 惰性 token 流：按需词法化，词法错误按恢复策略处理。
// ---------------------------------------------------------------------------

type tokenStream struct {
	lx        *lexer.Lexer
	input     string
	toks      []lexer.Token
	next      int // 下一个待词法化的字节偏移
	eof       bool
	truncated int // 快速失败词法错误截断流的字节偏移；-1 表示无
	s         *session
}

func (ts *tokenStream) get(i int) (lexer.Token, bool) {
	for len(ts.toks) <= i && !ts.eof {
		ts.fill()
	}
	if i < len(ts.toks) {
		return ts.toks[i], true
	}
	return lexer.Token{}, false
}

// forceAll 驱动词法流到输入末尾，返回 token 总数。
func (ts *tokenStream) forceAll() int {
	for !ts.eof {
		ts.fill()
	}
	return len(ts.toks)
}

func (ts *tokenStream) fill() {
	tok, npos, ok, err := ts.lx.Next(ts.input, ts.next)
	if err != nil {
		if le, isLE := err.(*lexer.Error); isLE {
			ts.s.addLexError(le.Offset, le.Msg)
		} else {
			ts.s.addLexError(ts.next, err.Error())
		}
		if ts.s.recovering() && !ts.s.errorLimitReached() {
			// 恢复：跳过一个字节继续词法化。
			ts.next++
			if ts.next > len(ts.input) {
				ts.eof = true
			}
			return
		}
		ts.eof = true // 快速失败：流在此截断
		ts.truncated = ts.next
		return
	}
	if !ok {
		ts.eof = true
		return
	}
	ts.toks = append(ts.toks, tok)
	ts.next = npos
}
