// Package generator 校验语法定义、检测冲突并生成可复用的解析器。
//
// 冲突检测策略（存在歧义时拒绝生成，绝不静默选择）：
//   - 结构校验：重复声明、未定义引用、空产生式、引用被忽略词法规则等；
//   - 候选分支按是否处于左递归位置划分为"种子分支"与"递归臂"；
//   - 种子分支之间要求 FIRST 集互不相交（FIRST/FIRST）；
//   - 可空种子分支要求与其余种子分支的 FOLLOW 无交（FIRST/FOLLOW）；
//   - 序列内部要求可空前缀与后续符号的 FIRST 无交；
//   - 递归臂的尾部（递归点之后的符号）之间 FIRST 互不相交，
//     且尾部 FIRST 不得与任何种子分支 FIRST 相交，
//     尾部右缘不得回到本规则（如 E = E "+" E 需改写为优先级分层）。
//
// 事实计算（nullable/FIRST/FOLLOW/左右缘）采用方程化工作清单算法：
// 每条规则的事实是其它规则事实的单调方程，仅当输入实际变化时才重算，
// 因此全量构建接近线性，增量校验只重算受影响闭包。
package generator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/guoyyit/parser-generator-go/grammar"
)

// EOFToken 是 FOLLOW 集中表示输入结束的特殊符号。
const EOFToken = "<EOF>"

// Severity 诊断级别。
type Severity int

const (
	// SeverityError 阻止生成的错误。
	SeverityError Severity = iota
	// SeverityWarning 不阻止生成的警告（如不可达规则）。
	SeverityWarning
)

// Diagnostic 是一条校验诊断。
type Diagnostic struct {
	Severity Severity
	Rule     string // 关联的规则名；全局问题为空串
	Msg      string
}

func (d Diagnostic) String() string {
	if d.Rule != "" {
		return fmt.Sprintf("%s: %s", d.Rule, d.Msg)
	}
	return d.Msg
}

// ConflictReport 是校验与冲突检测的结果。
type ConflictReport struct {
	Diagnostics []Diagnostic
}

// OK 报告是否可以生成（无错误级诊断）。
func (r *ConflictReport) OK() bool {
	if r == nil {
		return true
	}
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityError {
			return false
		}
	}
	return true
}

// Error 实现 error 接口，便于把报告当错误返回。
func (r *ConflictReport) Error() string {
	var b strings.Builder
	b.WriteString("grammar validation failed:")
	for _, d := range r.Diagnostics {
		fmt.Fprintf(&b, "\n  - %s", d)
	}
	return b.String()
}

func (r *ConflictReport) add(sev Severity, rule, format string, args ...any) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{
		Severity: sev,
		Rule:     rule,
		Msg:      fmt.Sprintf(format, args...),
	})
}

// AnalyzeOptions 控制增量分析。
type AnalyzeOptions struct {
	Prev       *Analysis       // 上一次分析结果（未受影响规则的事实复用）
	Active     map[string]bool // 需要重置并重算事实的规则闭包；nil 表示全部
	Changed    map[string]bool // 结构发生变化的规则（强制重新检查）
	PrevReport *ConflictReport // 上一次报告（未受影响规则的诊断结转）
	Index      *FactIndex      // 结构索引（增量校验器跨次复用）；nil 时现场构建
}

// Analysis 是对语法定义的分析结果，供生成与增量校验复用。
type Analysis struct {
	Rules     map[string]*grammar.Rule
	Nullable  map[string]bool
	First     map[string]map[string]bool
	Follow    map[string]map[string]bool
	Deps      map[string]map[string]bool // 规则 -> 直接引用的规则
	Reachable map[string]bool
}

// Analyze 对语法做全量校验与冲突检测。返回的分析结果无论是否通过都可用。
func Analyze(g *grammar.Grammar) (*Analysis, *ConflictReport) {
	return AnalyzeWith(g, nil)
}

// AnalyzeWith 执行校验与冲突检测；opts 非 nil 时按增量模式运行：
//   - 非 Active 规则的事实直接从 opts.Prev 复用（只读共享）；
//   - Active 规则的事实重置后按工作清单重算，输入实际变化才传播；
//   - 仅对"事实发生变化或结构发生变化"的规则重新执行冲突检查；
//   - opts.PrevReport 中未重新检查规则的诊断按规则名结转到新报告。
func AnalyzeWith(g *grammar.Grammar, opts *AnalyzeOptions) (*Analysis, *ConflictReport) {
	rep := &ConflictReport{}
	a := &Analysis{
		Rules:     map[string]*grammar.Rule{},
		Nullable:  map[string]bool{},
		First:     map[string]map[string]bool{},
		Follow:    map[string]map[string]bool{},
		Deps:      map[string]map[string]bool{},
		Reachable: map[string]bool{},
	}
	if g == nil {
		rep.add(SeverityError, "", "nil grammar")
		return a, rep
	}
	active := map[string]bool(nil)
	if opts != nil {
		active = opts.Active
	}
	structuralOK := checkStructural(g, a, rep, active)
	var recheck map[string]bool
	if structuralOK {
		eng := newFactEngine(g, a, opts)
		eng.run()
		recheck = eng.recheckSet()
		for i := range g.Rules {
			if !recheck[g.Rules[i].Name] {
				continue
			}
			checkRuleConflicts(g, a, &g.Rules[i], rep)
		}
		checkReachability(g, a, rep)
	}
	// 结转未重新检查规则的历史诊断（结构校验失败时不结转，
	// 此时报告只包含结构性问题）。
	if opts != nil && opts.PrevReport != nil && recheck != nil {
		for _, d := range opts.PrevReport.Diagnostics {
			if d.Rule == "" {
				continue // 全局诊断每次全量重算，不结转
			}
			if recheck[d.Rule] {
				continue
			}
			if _, stillExists := a.Rules[d.Rule]; !stillExists {
				continue
			}
			rep.Diagnostics = append(rep.Diagnostics, d)
		}
	}
	return a, rep
}

// ---------------------------------------------------------------------------
// 结构校验
// ---------------------------------------------------------------------------

func checkStructural(g *grammar.Grammar, a *Analysis, rep *ConflictReport, active map[string]bool) bool {
	ok := true
	lexerNames := map[string]bool{}
	ignored := map[string]bool{}
	for _, lr := range g.LexerRules {
		if lr.Name == "" {
			rep.add(SeverityError, "", "lexer rule with empty name")
			ok = false
			continue
		}
		if lexerNames[lr.Name] {
			rep.add(SeverityError, "", "duplicate lexer rule %q", lr.Name)
			ok = false
			continue
		}
		lexerNames[lr.Name] = true
		if lr.Pattern == "" {
			rep.add(SeverityError, "", "lexer rule %q has empty pattern", lr.Name)
			ok = false
		}
		if lr.Ignore {
			ignored[lr.Name] = true
		}
	}
	for i := range g.Rules {
		r := &g.Rules[i]
		if r.Name == "" {
			rep.add(SeverityError, "", "rule with empty name")
			ok = false
			continue
		}
		if _, dup := a.Rules[r.Name]; dup {
			rep.add(SeverityError, r.Name, "duplicate rule declaration")
			ok = false
			continue
		}
		a.Rules[r.Name] = r
		if len(r.Alternatives) == 0 {
			rep.add(SeverityError, r.Name, "rule has no alternatives")
			ok = false
		}
	}
	if g.Start == "" {
		rep.add(SeverityError, "", "no start rule specified")
		ok = false
	} else if _, ok2 := a.Rules[g.Start]; !ok2 {
		rep.add(SeverityError, g.Start, "start rule %q is not defined", g.Start)
		ok = false
	}
	// 引用解析只对活跃规则逐项检查（active 为 nil 时全量）；
	// 未修改规则的结构问题只会由它引用的规则被删除引入，
	// 此时该规则已在活跃闭包（按旧依赖图反向扩展）内。
	for i := range g.Rules {
		r := &g.Rules[i]
		if active != nil && !active[r.Name] {
			continue
		}
		for _, alt := range r.Alternatives {
			walkTerms(alt.Terms, func(t *grammar.Term) {
				switch t.Kind {
				case grammar.TermNonTerminal:
					if _, found := a.Rules[t.Name]; !found {
						rep.add(SeverityError, r.Name, "references undefined rule %q", t.Name)
						ok = false
					}
				case grammar.TermTerminal:
					if !lexerNames[t.Name] {
						rep.add(SeverityError, r.Name, "references undefined token %q", t.Name)
						ok = false
					} else if ignored[t.Name] {
						rep.add(SeverityError, r.Name, "references ignored lexer rule %q", t.Name)
						ok = false
					}
				}
			})
		}
	}
	return ok
}

// walkTerms 遍历分支中的所有 Term（含子表达式内部）。
func walkTerms(terms []grammar.Term, fn func(*grammar.Term)) {
	for i := range terms {
		t := &terms[i]
		fn(t)
		if t.Sub != nil {
			walkTerms(t.Sub.Terms, fn)
		}
	}
}

// ---------------------------------------------------------------------------
// 冲突检测
// ---------------------------------------------------------------------------

type altInfo struct {
	index    int
	first    map[string]bool
	nullable bool
	// 仅递归臂：递归点之后的尾部信息
	tailFirst map[string]bool
	tailReach bool // 尾部右缘是否回到本规则
}

func checkRuleConflicts(g *grammar.Grammar, a *Analysis, r *grammar.Rule, rep *ConflictReport) {
	type pair struct{ x, y int }

	// 划分种子分支与递归臂。
	type seed struct {
		index    int
		nullable bool
	}
	type arm struct {
		index     int
		tailReach bool
	}
	var seeds []seed
	var arms []arm
	for i, alt := range r.Alternatives {
		if recPoint := a.recursionPoint(r.Name, alt.Terms); recPoint >= 0 {
			if recPoint > 0 && a.seqDerivesTerminal(alt.Terms[:recPoint], map[string]bool{}) {
				rep.add(SeverityError, r.Name,
					"alternative #%d has left recursion behind a non-empty prefix; greedy runtimes cannot resolve it deterministically: restructure so the recursive symbol is leftmost",
					i)
			}
			tail := alt.Terms[recPoint+1:]
			arms = append(arms, arm{index: i, tailReach: a.rightEdgeReaches(r.Name, tail)})
			continue
		}
		seeds = append(seeds, seed{index: i, nullable: a.seqNullable(alt.Terms)})
	}
	if len(arms) > 0 && len(seeds) == 0 {
		rep.add(SeverityError, r.Name, "left-recursive rule has no base (non-recursive) alternative")
	}

	// 种子 token 归属与种子/种子冲突（线性枚举，不构造分支集合）。
	seedOwner := map[string]int{}
	seedConflicts := map[pair]map[string]bool{}
	for _, sd := range seeds {
		a.forEachFirstToken(r.Alternatives[sd.index].Terms, func(tok string) {
			if j, ok := seedOwner[tok]; ok {
				k := pair{j, sd.index}
				if seedConflicts[k] == nil {
					seedConflicts[k] = map[string]bool{}
				}
				seedConflicts[k][tok] = true
			} else {
				seedOwner[tok] = sd.index
			}
		})
	}
	for k, toks := range seedConflicts {
		rep.add(SeverityError, r.Name,
			"alternatives #%d and #%d are ambiguous: both can start with %s (FIRST/FIRST conflict)",
			k.x, k.y, listSet(toks))
	}
	// 可空种子：至多一个，且不得与其他种子的 FIRST 在 FOLLOW 上重叠。
	var nullableSeed *seed
	for i := range seeds {
		if !seeds[i].nullable {
			continue
		}
		if nullableSeed != nil {
			rep.add(SeverityError, r.Name,
				"alternatives #%d and #%d are both nullable; at most one empty alternative is allowed",
				nullableSeed.index, seeds[i].index)
		}
		nullableSeed = &seeds[i]
	}
	if nullableSeed != nil {
		for _, sd := range seeds {
			if sd.nullable {
				continue
			}
			hit := map[string]bool{}
			a.forEachFirstToken(r.Alternatives[sd.index].Terms, func(tok string) {
				if a.Follow[r.Name][tok] {
					hit[tok] = true
				}
			})
			if len(hit) > 0 {
				rep.add(SeverityError, r.Name,
					"nullable alternative #%d conflicts with alternative #%d: %s can follow %s (FIRST/FOLLOW conflict)",
					nullableSeed.index, sd.index, listSet(hit), r.Name)
			}
		}
	}
	// 递归臂：臂/臂 尾部 FIRST 冲突（保守判定，宁可拒绝不可证确定性的文法）。
	armOwner := map[string]int{}
	armConflicts := map[pair]map[string]bool{}
	for _, am := range arms {
		if am.tailReach {
			rep.add(SeverityError, r.Name,
				"alternative #%d is ambiguous: the part after the recursion point can itself end with %s; introduce a precedence level (e.g. %s = %s OP %sSub | %sSub)",
				am.index, r.Name, r.Name, r.Name, r.Name, r.Name)
		}
		// 找到该臂的递归点以枚举尾部 FIRST。
		recPoint := a.recursionPoint(r.Name, r.Alternatives[am.index].Terms)
		tail := r.Alternatives[am.index].Terms[recPoint+1:]
		a.forEachFirstToken(tail, func(tok string) {
			if j, ok := armOwner[tok]; ok {
				k := pair{j, am.index}
				if armConflicts[k] == nil {
					armConflicts[k] = map[string]bool{}
				}
				armConflicts[k][tok] = true
			} else {
				armOwner[tok] = am.index
			}
		})
	}
	for k, toks := range armConflicts {
		rep.add(SeverityError, r.Name,
			"recursive alternatives #%d and #%d are ambiguous: both can continue with %s",
			k.x, k.y, listSet(toks))
	}
	// 序列内部 LL(1) 检查（含子表达式）。
	for _, alt := range r.Alternatives {
		a.checkSeq(r, alt.Terms, a.Follow[r.Name], rep)
	}
}

// recursionPoint 返回分支中左递归点的下标；非递归分支返回 -1。
// 递归点是最左的、左缘包含本规则且前缀全部可空的 Term。
// 左缘可达性按查询做 DFS（带 visited 剪枝），不物化传递闭包。
func (a *Analysis) recursionPoint(rule string, terms []grammar.Term) int {
	visited := map[string]bool{}
	for i := range terms {
		if a.termLeftEdgeReaches(&terms[i], rule, visited) {
			return i
		}
		if !a.termNullable(&terms[i]) {
			return -1
		}
	}
	return -1
}

// termLeftEdgeReaches 报告某 Term 的左缘是否可到达指定规则。
func (a *Analysis) termLeftEdgeReaches(t *grammar.Term, target string, visited map[string]bool) bool {
	switch t.Kind {
	case grammar.TermNonTerminal:
		if t.Name == target {
			return true
		}
		return a.ruleLeftEdgeReaches(t.Name, target, visited)
	case grammar.TermOptional, grammar.TermZeroOrMore, grammar.TermOneOrMore, grammar.TermGroup:
		return a.seqLeftEdgeReaches(t.Sub.Terms, target, visited)
	}
	return false
}

func (a *Analysis) ruleLeftEdgeReaches(rule, target string, visited map[string]bool) bool {
	if visited[rule] {
		return false
	}
	visited[rule] = true
	r := a.Rules[rule]
	if r == nil {
		return false
	}
	for i := range r.Alternatives {
		if a.seqLeftEdgeReaches(r.Alternatives[i].Terms, target, visited) {
			return true
		}
	}
	return false
}

func (a *Analysis) seqLeftEdgeReaches(terms []grammar.Term, target string, visited map[string]bool) bool {
	for i := range terms {
		if a.termLeftEdgeReaches(&terms[i], target, visited) {
			return true
		}
		if !a.termNullable(&terms[i]) {
			return false
		}
	}
	return false
}

// seqDerivesTerminal 报告序列是否可推出包含终结符的串（DFS 查询）。
func (a *Analysis) seqDerivesTerminal(terms []grammar.Term, visited map[string]bool) bool {
	for i := range terms {
		t := &terms[i]
		switch t.Kind {
		case grammar.TermTerminal:
			return true
		case grammar.TermNonTerminal:
			if visited[t.Name] {
				continue
			}
			visited[t.Name] = true
			r := a.Rules[t.Name]
			if r == nil {
				continue
			}
			for _, alt := range r.Alternatives {
				if a.seqDerivesTerminal(alt.Terms, visited) {
					return true
				}
			}
		case grammar.TermOptional, grammar.TermZeroOrMore, grammar.TermOneOrMore, grammar.TermGroup:
			if a.seqDerivesTerminal(t.Sub.Terms, visited) {
				return true
			}
		}
	}
	return false
}

// rightEdgeReaches 报告序列右缘是否可到达指定规则（DFS 查询）。
func (a *Analysis) rightEdgeReaches(rule string, terms []grammar.Term) bool {
	return a.seqRightEdgeReaches(terms, rule, map[string]bool{})
}

func (a *Analysis) seqRightEdgeReaches(terms []grammar.Term, target string, visited map[string]bool) bool {
	for i := len(terms) - 1; i >= 0; i-- {
		if a.termRightEdgeReaches(&terms[i], target, visited) {
			return true
		}
		if !a.termNullable(&terms[i]) {
			return false
		}
	}
	return false
}

func (a *Analysis) termRightEdgeReaches(t *grammar.Term, target string, visited map[string]bool) bool {
	switch t.Kind {
	case grammar.TermNonTerminal:
		if t.Name == target {
			return true
		}
		return a.ruleRightEdgeReaches(t.Name, target, visited)
	case grammar.TermOptional, grammar.TermZeroOrMore, grammar.TermOneOrMore, grammar.TermGroup:
		return a.seqRightEdgeReaches(t.Sub.Terms, target, visited)
	}
	return false
}

func (a *Analysis) ruleRightEdgeReaches(rule, target string, visited map[string]bool) bool {
	if visited[rule] {
		return false
	}
	visited[rule] = true
	r := a.Rules[rule]
	if r == nil {
		return false
	}
	for i := range r.Alternatives {
		if a.seqRightEdgeReaches(r.Alternatives[i].Terms, target, visited) {
			return true
		}
	}
	return false
}

// checkSeq 检查序列内部的可空前缀与后续符号的 FIRST 交叠，并递归子表达式。
func (a *Analysis) checkSeq(r *grammar.Rule, terms []grammar.Term, seqFollow map[string]bool, rep *ConflictReport) {
	for i := range terms {
		t := &terms[i]
		if t.Sub != nil {
			// 分组/量词：需要后续上下文做子表达式检查。
			restFirst := a.firstOfSeq(terms[i+1:])
			if a.seqNullable(terms[i+1:]) {
				restFirst = union(restFirst, seqFollow)
			}
			subFollow := restFirst
			if t.Kind == grammar.TermZeroOrMore || t.Kind == grammar.TermOneOrMore {
				subFollow = union(subFollow, a.firstOfSeq(t.Sub.Terms))
			}
			a.checkSeq(r, t.Sub.Terms, subFollow, rep)
		}
		if !a.termNullable(t) {
			continue
		}
		restFirst := a.firstOfSeq(terms[i+1:])
		if a.seqNullable(terms[i+1:]) {
			restFirst = union(restFirst, seqFollow)
		}
		if inter := intersect(a.firstOfTerm(t), restFirst); len(inter) > 0 {
			rep.add(SeverityError, r.Name,
				"optional/repeated element at position %d can start with %s which may also follow it (ambiguity)",
				i, listSet(inter))
		}
	}
}

// checkReachability 标记不可达规则（警告）与不可产生规则（错误）。
func checkReachability(g *grammar.Grammar, a *Analysis, rep *ConflictReport) {
	if g.Start != "" {
		var visit func(string)
		visit = func(name string) {
			if a.Reachable[name] {
				return
			}
			a.Reachable[name] = true
			for dep := range a.Deps[name] {
				visit(dep)
			}
		}
		visit(g.Start)
	}
	for i := range g.Rules {
		if !a.Reachable[g.Rules[i].Name] {
			rep.add(SeverityWarning, g.Rules[i].Name, "rule is unreachable from start rule")
		}
	}
	// 可产生性：反向工作清单，O(V+E)。
	// 每条分支记录阻塞计数（未确认可产生的非终结符出现次数，
	// Optional/ZeroOrMore 内部不阻塞）；计数归零则规则可产生并向前驱传播。
	type altRef struct {
		rule string
		alt  int
	}
	uses := map[string][]altRef{}
	counts := map[string][]int{}
	var countBlocking func(rule string, ai int, terms []grammar.Term) int
	countBlocking = func(rule string, ai int, terms []grammar.Term) int {
		cnt := 0
		for i := range terms {
			t := &terms[i]
			switch t.Kind {
			case grammar.TermNonTerminal:
				cnt++
				uses[t.Name] = append(uses[t.Name], altRef{rule: rule, alt: ai})
			case grammar.TermOneOrMore, grammar.TermGroup:
				cnt += countBlocking(rule, ai, t.Sub.Terms)
			}
		}
		return cnt
	}
	productive := map[string]bool{}
	queue := make([]string, 0, len(g.Rules))
	for i := range g.Rules {
		r := &g.Rules[i]
		counts[r.Name] = make([]int, len(r.Alternatives))
		for ai := range r.Alternatives {
			counts[r.Name][ai] = countBlocking(r.Name, ai, r.Alternatives[ai].Terms)
		}
	}
	for i := range g.Rules {
		r := &g.Rules[i]
		for ai := range r.Alternatives {
			if counts[r.Name][ai] == 0 {
				if !productive[r.Name] {
					productive[r.Name] = true
					queue = append(queue, r.Name)
				}
				break
			}
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, use := range uses[x] {
			if productive[use.rule] {
				continue
			}
			counts[use.rule][use.alt]--
			if counts[use.rule][use.alt] == 0 {
				productive[use.rule] = true
				queue = append(queue, use.rule)
			}
		}
	}
	for i := range g.Rules {
		if a.Reachable[g.Rules[i].Name] && !productive[g.Rules[i].Name] {
			rep.add(SeverityError, g.Rules[i].Name, "rule can never derive a terminal string (unproductive)")
		}
	}
}

// ---------------------------------------------------------------------------
// 共享的序列级助手（供冲突检查与事实引擎使用）
// ---------------------------------------------------------------------------

func (a *Analysis) seqNullable(terms []grammar.Term) bool {
	for i := range terms {
		if !a.termNullable(&terms[i]) {
			return false
		}
	}
	return true
}

func (a *Analysis) termNullable(t *grammar.Term) bool {
	switch t.Kind {
	case grammar.TermTerminal:
		return false
	case grammar.TermNonTerminal:
		return a.Nullable[t.Name]
	case grammar.TermOptional, grammar.TermZeroOrMore:
		return true
	case grammar.TermOneOrMore, grammar.TermGroup:
		return a.seqNullable(t.Sub.Terms)
	}
	return false
}

func (a *Analysis) firstOfTerm(t *grammar.Term) map[string]bool {
	out := map[string]bool{}
	a.absorbFirstOfSeq(out, []grammar.Term{*t})
	return out
}

func (a *Analysis) firstOfSeq(terms []grammar.Term) map[string]bool {
	out := map[string]bool{}
	a.absorbFirstOfSeq(out, terms)
	return out
}

// forEachFirstToken 逐个枚举序列可起始的终结符（不分配集合）。
func (a *Analysis) forEachFirstToken(terms []grammar.Term, fn func(string)) {
	for i := range terms {
		t := &terms[i]
		switch t.Kind {
		case grammar.TermTerminal:
			fn(t.Name)
			return
		case grammar.TermNonTerminal:
			for tok := range a.First[t.Name] {
				fn(tok)
			}
			if !a.Nullable[t.Name] {
				return
			}
		case grammar.TermOptional, grammar.TermZeroOrMore:
			a.forEachFirstToken(t.Sub.Terms, fn)
		case grammar.TermOneOrMore, grammar.TermGroup:
			a.forEachFirstToken(t.Sub.Terms, fn)
			if !a.seqNullable(t.Sub.Terms) {
				return
			}
		}
	}
}

// absorbFirstOfSeq 把序列的 FIRST 并入 dst。
func (a *Analysis) absorbFirstOfSeq(dst map[string]bool, terms []grammar.Term) {
	for i := range terms {
		t := &terms[i]
		switch t.Kind {
		case grammar.TermTerminal:
			dst[t.Name] = true
			return
		case grammar.TermNonTerminal:
			absorb(dst, a.First[t.Name])
			if !a.Nullable[t.Name] {
				return
			}
		case grammar.TermOptional, grammar.TermZeroOrMore:
			a.absorbFirstOfSeq(dst, t.Sub.Terms)
		case grammar.TermOneOrMore, grammar.TermGroup:
			a.absorbFirstOfSeq(dst, t.Sub.Terms)
			if !a.seqNullable(t.Sub.Terms) {
				return
			}
		}
	}
}

func absorb(dst, src map[string]bool) {
	for k := range src {
		dst[k] = true
	}
}

func union(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func intersect(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

func setEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func listSet(s map[string]bool) string {
	items := make([]string, 0, len(s))
	for k := range s {
		items = append(items, k)
	}
	sort.Strings(items)
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = fmt.Sprintf("%q", it)
	}
	return strings.Join(quoted, ", ")
}
