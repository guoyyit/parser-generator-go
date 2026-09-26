package generator

import (
	"github.com/guoyyit/parser-generator-go/grammar"
)

// 事实种类（位掩码），用于变化记录与传播。
// 左右缘可达性不物化，改为按需 DFS 查询（见 analysis.go），
// 因此这里只有 nullable / FIRST / FOLLOW 三类物化事实。
const (
	fNullable uint8 = 1 << iota
	fFirst
	fFollow
)

// followSite 定位一个非终结符在语法中的一次出现（用于 FOLLOW 方程）。
type followSite struct {
	rule string
	alt  int
	path []int // 从规则分支到该出现的下标路径（穿过分组/量词）
}

// factTask 是工作清单中的一项：重算某规则的某类事实。
type factTask struct {
	rule string
	kind uint8
}

// factEngine 是方程化的事实计算引擎。
//
// 每条规则的事实表示为其它规则事实的单调方程：
//
//	nullable(r) = ∨ 分支全可空
//	FIRST(r)    = ∪ 分支左缘 FIRST
//	FOLLOW(x)   = ∪ 使用位置的上下文（FIRST(rest) ∪ 可空时 FOLLOW(属主)）
//
// 工作清单只在某事实的值实际变化时才把依赖者重新入队，
// 因此全量计算接近线性，增量模式只重算 Active 闭包。
type factEngine struct {
	g          *grammar.Grammar
	a          *Analysis
	prev       *Analysis
	active     map[string]bool // nil 表示全部规则
	changedOpt map[string]bool
	ix         *FactIndex // 静态结构索引（可跨校验增量复用）

	queue   []factTask
	pending map[factTask]bool
}

func newFactEngine(g *grammar.Grammar, a *Analysis, opts *AnalyzeOptions) *factEngine {
	e := &factEngine{
		g:       g,
		a:       a,
		pending: make(map[factTask]bool, len(g.Rules)*3),
	}
	if opts != nil {
		e.prev = opts.Prev
		e.active = opts.Active
		e.changedOpt = opts.Changed
		e.ix = opts.Index
	}
	if e.ix == nil {
		e.ix = BuildFactIndex(g)
	}
	// 快照依赖图：FactIndex 会被后续校验增量修改，
	// Analysis（可能挂在已发布产物上）必须保持不可变。
	a.Deps = copyDeps(e.ix.deps)
	e.initFacts()
	return e
}

func copyDeps(in map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(in))
	for k, v := range in {
		cp := make(map[string]bool, len(v))
		for d := range v {
			cp[d] = true
		}
		out[k] = cp
	}
	return out
}

func (e *factEngine) isActive(name string) bool {
	return e.active == nil || e.active[name]
}

// activateInactive 把不在活跃闭包中的规则动态纳入活跃集：
// 重置其全部事实并入队重算。仅增量模式（active 非 nil）下生效。
func (e *factEngine) activateInactive(deps map[string]bool) {
	if e.active == nil {
		return
	}
	for x := range deps {
		if e.active[x] {
			continue
		}
		if _, ok := e.a.Rules[x]; !ok {
			continue
		}
		e.active[x] = true
		e.a.Nullable[x] = false
		e.a.First[x] = map[string]bool{}
		e.a.Follow[x] = map[string]bool{}
		if x == e.g.Start {
			e.a.Follow[x][EOFToken] = true
		}
		e.enqueue(x, fNullable)
		e.enqueue(x, fFirst)
		e.enqueue(x, fFollow)
	}
}

// initFacts 初始化事实集合：活跃规则的事实留空（求值时按需分配），
// 非活跃规则复用历史值。
func (e *factEngine) initFacts() {
	for name := range e.a.Rules {
		if e.isActive(name) || e.prev == nil {
			continue
		}
		e.a.Nullable[name] = e.prev.Nullable[name]
		e.a.First[name] = e.prev.First[name]
		e.a.Follow[name] = e.prev.Follow[name]
	}
	// 起始规则的 FOLLOW 恒含 EOF。
	if e.g.Start != "" && e.isActive(e.g.Start) {
		e.a.Follow[e.g.Start] = map[string]bool{EOFToken: true}
	}
}

// ---------------------------------------------------------------------------
// 工作清单求值
// ---------------------------------------------------------------------------

func (e *factEngine) enqueue(rule string, kind uint8) {
	if !e.isActive(rule) {
		return
	}
	t := factTask{rule: rule, kind: kind}
	if e.pending[t] {
		return
	}
	e.pending[t] = true
	e.queue = append(e.queue, t)
}

func (e *factEngine) run() {
	for name := range e.a.Rules {
		e.enqueue(name, fNullable)
		e.enqueue(name, fFirst)
		e.enqueue(name, fFollow)
	}
	for len(e.queue) > 0 {
		t := e.queue[0]
		e.queue = e.queue[1:]
		delete(e.pending, t)
		switch t.kind {
		case fNullable:
			e.evalNullable(t.rule)
		case fFirst:
			e.evalFirst(t.rule)
		case fFollow:
			e.evalFollow(t.rule)
		}
	}
}

func (e *factEngine) evalNullable(r string) {
	rule := e.a.Rules[r]
	val := false
	for _, alt := range rule.Alternatives {
		if e.a.seqNullable(alt.Terms) {
			val = true
			break
		}
	}
	if val != e.a.Nullable[r] {
		e.a.Nullable[r] = val
		// nullable 变化影响：任意位置引用者的 nullable、左缘依赖者的 FIRST、
		// 以及 FOLLOW 方程读取 nullable 的规则。
		for x := range e.ix.allRev[r] {
			e.enqueue(x, fNullable)
		}
		for x := range e.ix.firstRev[r] {
			e.enqueue(x, fFirst)
		}
		for x := range e.ix.fctxRev[r] {
			e.enqueue(x, fFollow)
		}
	}
	// 与历史值出现任何偏差（增长或收缩）时，激活未在闭包内的依赖者，
	// 由它们重置后重算，避免沿旧值留下过期事实。
	if e.prev != nil && val != e.prev.Nullable[r] {
		e.activateInactive(e.ix.allRev[r])
		e.activateInactive(e.ix.firstRev[r])
		e.activateInactive(e.ix.fctxRev[r])
	}
}

func (e *factEngine) evalFirst(r string) {
	rule := e.a.Rules[r]
	set := map[string]bool{}
	for _, alt := range rule.Alternatives {
		e.a.absorbFirstOfSeq(set, alt.Terms)
	}
	if !setEqual(set, e.a.First[r]) {
		e.a.First[r] = set
		for x := range e.ix.firstRev[r] {
			e.enqueue(x, fFirst)
		}
		for x := range e.ix.fctxRev[r] {
			e.enqueue(x, fFollow)
		}
	}
	if e.prev != nil && !setEqual(set, e.prev.First[r]) {
		e.activateInactive(e.ix.firstRev[r])
		e.activateInactive(e.ix.fctxRev[r])
	}
}

func (e *factEngine) evalFollow(r string) {
	set := map[string]bool{}
	if r == e.g.Start {
		set[EOFToken] = true
	}
	for _, site := range e.ix.followSites[r] {
		absorb(set, e.followContribution(site))
	}
	if !setEqual(set, e.a.Follow[r]) {
		e.a.Follow[r] = set
		// FOLLOW(r) 变化影响 r 内出现的所有非终结符的 FOLLOW。
		for x := range e.ix.deps[r] {
			e.enqueue(x, fFollow)
		}
	}
	if e.prev != nil && !setEqual(set, e.prev.Follow[r]) {
		e.activateInactive(e.ix.deps[r])
	}
}

// followContribution 计算某出现位置对 FOLLOW 的贡献。
func (e *factEngine) followContribution(site followSite) map[string]bool {
	r := e.a.Rules[site.rule]
	return e.ctxInSeq(r.Alternatives[site.alt].Terms, site.path, e.a.Follow[site.rule])
}

// ctxInSeq 自外向内计算路径处符号的 FOLLOW 上下文。
func (e *factEngine) ctxInSeq(terms []grammar.Term, path []int, enclosing map[string]bool) map[string]bool {
	idx := path[0]
	t := &terms[idx]
	rest := terms[idx+1:]
	c := e.a.firstOfSeq(rest)
	if e.a.seqNullable(rest) {
		c = union(c, enclosing)
	}
	if len(path) == 1 {
		return c
	}
	enc := c
	if t.Kind == grammar.TermZeroOrMore || t.Kind == grammar.TermOneOrMore {
		enc = union(enc, e.a.firstOfSeq(t.Sub.Terms))
	}
	return e.ctxInSeq(t.Sub.Terms, path[1:], enc)
}

// ---------------------------------------------------------------------------
// 重检集合：事实或结构发生变化、或其引用的事实发生变化的规则
// ---------------------------------------------------------------------------

func (e *factEngine) recheckSet() map[string]bool {
	out := map[string]bool{}
	if e.prev == nil {
		for name := range e.a.Rules {
			out[name] = true
		}
		return out
	}
	changedFacts := map[string]uint8{}
	for name := range e.a.Rules {
		if !e.isActive(name) {
			continue
		}
		var mask uint8
		if e.a.Nullable[name] != e.prev.Nullable[name] {
			mask |= fNullable
		}
		if !setEqual(e.a.First[name], e.prev.First[name]) {
			mask |= fFirst
		}
		if !setEqual(e.a.Follow[name], e.prev.Follow[name]) {
			mask |= fFollow
		}
		if mask != 0 {
			changedFacts[name] = mask
		}
	}
	// nullable 变化会影响左/右缘 DFS 查询的结论，进而影响所有
	// 传递引用者的冲突检查，因此沿依赖反向闭包传播重检。
	nullableAffected := map[string]bool{}
	var stack []string
	for name, mask := range changedFacts {
		if mask&fNullable != 0 {
			nullableAffected[name] = true
			stack = append(stack, name)
		}
	}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for user := range e.ix.allRev[x] {
			if !nullableAffected[user] {
				nullableAffected[user] = true
				stack = append(stack, user)
			}
		}
	}
	for name := range e.a.Rules {
		if e.changedOpt[name] || changedFacts[name] != 0 || nullableAffected[name] {
			out[name] = true
			continue
		}
		// 直接引用的规则 FIRST 变化也会影响本规则的序列检查结论。
		for dep := range e.ix.deps[name] {
			if changedFacts[dep]&fFirst != 0 {
				out[name] = true
				break
			}
		}
	}
	return out
}
