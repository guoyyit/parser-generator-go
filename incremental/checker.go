// Package incremental 提供语法定义的增量校验：对局部修改只重新计算
// 受影响的规则，未受影响规则的分析事实与诊断直接复用，
// 使校验与生成的总耗时明显低于全量重建。
//
// 影响集计算：
//   - C  = 内容发生变化的规则（新增 / 删除 / 规范化哈希不同）；
//   - D  = C 的传递依赖者（FIRST/nullable 可能变化的规则）；
//   - F  = C ∪ D ∪ deps*(C ∪ D)（FOLLOW 可能沿右缘向下传播）；
//   - 仅对 F 重算事实与冲突检查，其余规则的诊断按名结转。
//
// 词法规则或起始规则发生变化时回退为全量校验（词法部分通常很小）。
package incremental

import (
	"github.com/guoyyit/parser-generator-go/generator"
	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/lexer"
	"github.com/guoyyit/parser-generator-go/parser"
	"github.com/guoyyit/parser-generator-go/version"
)

// Checker 是有状态的增量校验器。Checker 本身不可并发使用；
// 但产出的 Artifact 与报告可安全共享。
type Checker struct {
	g            *grammar.Grammar
	ana          *generator.Analysis
	rep          *generator.ConflictReport
	art          *generator.Artifact
	lx           *lexer.Lexer
	hashes       map[string]string
	fp           version.Fingerprint
	index        *generator.FactIndex // 结构索引（跨次增量复用）
	lastAffected map[string]bool
}

// NewChecker 以全量校验初始化检查器。
func NewChecker(g *grammar.Grammar) (*Checker, *generator.ConflictReport, error) {
	c := &Checker{}
	rep, err := c.full(g)
	if err != nil {
		return nil, nil, err
	}
	return c, rep, nil
}

// Update 应用一份（可能与上次仅局部不同的）语法定义并返回校验报告。
// 报告 OK 时可通过 Artifact 获取重新生成的解析器。
func (c *Checker) Update(g *grammar.Grammar) (*generator.ConflictReport, error) {
	if g == nil {
		return nil, errNilGrammar()
	}
	// 词法或起始规则变化：全量。词法比较为逐字段结构比较（O(L)，无哈希）。
	if c.g == nil || g.Start != c.g.Start || !lexerRulesEqual(g.LexerRules, c.g.LexerRules) {
		return c.full(g)
	}
	newHashes := ruleHashes(g)
	changed := map[string]bool{}
	for name, h := range newHashes {
		if c.hashes[name] != h {
			changed[name] = true
		}
	}
	for name := range c.hashes {
		if _, ok := newHashes[name]; !ok {
			changed[name] = true // 删除的规则
		}
	}
	if len(changed) == 0 {
		return c.rep, nil // 无变化，直接返回缓存报告
	}
	affected := c.affectedSet(g, changed)
	// 结构索引增量打补丁（在 affectedSet 之后，依赖旧索引做反向扩展）。
	c.index.Patch(g, c.g, changed)
	ana, rep := generator.AnalyzeWith(g, &generator.AnalyzeOptions{
		Prev:       c.ana,
		Active:     affected,
		Changed:    changed,
		PrevReport: c.rep,
		Index:      c.index,
	})
	// 分段增量维护指纹。
	fp := c.fp
	for name := range changed {
		if oldRule := c.g.FindRule(name); oldRule != nil {
			fp.XOR(version.HashRule(*oldRule))
		}
		if newRule := g.FindRule(name); newRule != nil {
			fp.XOR(version.HashRule(*newRule))
		}
	}
	c.commit(g, ana, rep, newHashes, affected, fp)
	return rep, nil
}

// Artifact 返回当前语法对应的生成产物；报告非 OK 时为 nil。
func (c *Checker) Artifact() *generator.Artifact { return c.art }

// Report 返回最近一次校验报告。
func (c *Checker) Report() *generator.ConflictReport { return c.rep }

// LastAffected 返回最近一次增量更新实际重算的规则集（用于观测增量效果）。
func (c *Checker) LastAffected() map[string]bool {
	out := make(map[string]bool, len(c.lastAffected))
	for k := range c.lastAffected {
		out[k] = true
	}
	return out
}

// ---------------------------------------------------------------------------

func (c *Checker) full(g *grammar.Grammar) (*generator.ConflictReport, error) {
	if g == nil {
		return nil, errNilGrammar()
	}
	ana, rep := generator.Analyze(g)
	lx, lxErr := lexer.Compile(g.LexerRules)
	if lxErr != nil && rep.OK() {
		rep = &generator.ConflictReport{}
	}
	c.lx = nil
	if lxErr == nil {
		c.lx = lx
	}
	hashes := ruleHashes(g)
	c.index = generator.BuildFactIndex(g)
	c.commit(g, ana, rep, hashes, nil, version.FingerprintOf(g))
	return rep, nil
}

func (c *Checker) commit(g *grammar.Grammar, ana *generator.Analysis, rep *generator.ConflictReport, hashes map[string]string, affected map[string]bool, fp version.Fingerprint) {
	// 单次克隆，之后的快照、解析器与产物共享这份不可变副本。
	gc := g.Clone()
	c.g = gc
	c.ana = ana
	c.rep = rep
	c.hashes = hashes
	c.fp = fp
	c.lastAffected = affected
	c.art = nil
	if !rep.OK() {
		return
	}
	// 词法器复用：词法未变时沿用已编译的 Lexer。
	lx := c.lx
	if lx == nil {
		var err error
		lx, err = lexer.Compile(gc.LexerRules)
		if err != nil {
			return
		}
	}
	p, err := parser.NewShared(gc, lx, gc.Start)
	if err != nil {
		return
	}
	c.lx = lx
	c.art = generator.NewArtifactWithFingerprint(p, gc, ana, fp)
}

// affectedSet 计算需要重算的初始闭包：C ∪ dependents*(C)，
// 同时返回增量更新后的依赖图（仅变化规则重算，其余沿用缓存）。
// 依赖者闭包通过反向邻接表 BFS 计算，整体 O(V+E)。
// 更深层的 FOLLOW 传播由事实引擎在检测到实际偏差时动态激活，
// 无需在此静态保守扩展。
func (c *Checker) affectedSet(g *grammar.Grammar, changed map[string]bool) map[string]bool {
	newDeps := c.patchDeps(g, changed)
	// 反向邻接表（新旧依赖图取并集，覆盖规则被删除的情形）。
	rev := map[string][]string{}
	addRev := func(deps map[string]map[string]bool) {
		for user, ds := range deps {
			for d := range ds {
				rev[d] = append(rev[d], user)
			}
		}
	}
	addRev(newDeps)
	if c.index != nil {
		addRev(c.index.Deps())
	}
	dependents := map[string]bool{}
	queue := make([]string, 0, len(changed))
	for name := range changed {
		if !dependents[name] {
			dependents[name] = true
			queue = append(queue, name)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, user := range rev[x] {
			if !dependents[user] {
				dependents[user] = true
				queue = append(queue, user)
			}
		}
	}
	// 只保留仍然存在的规则。
	for name := range dependents {
		if g.FindRule(name) == nil {
			delete(dependents, name)
		}
	}
	return dependents
}

// patchDeps 复用缓存的依赖图，仅重算变化规则的依赖；新增规则补入，删除规则剔除。
func (c *Checker) patchDeps(g *grammar.Grammar, changed map[string]bool) map[string]map[string]bool {
	if c.index == nil {
		return generator.ComputeDeps(g)
	}
	existing := make(map[string]bool, len(g.Rules))
	byName := make(map[string]*grammar.Rule, len(g.Rules))
	for i := range g.Rules {
		existing[g.Rules[i].Name] = true
		byName[g.Rules[i].Name] = &g.Rules[i]
	}
	out := make(map[string]map[string]bool, len(g.Rules))
	for name, deps := range c.index.Deps() {
		if !changed[name] && existing[name] {
			out[name] = deps
		}
	}
	for name := range changed {
		r := byName[name]
		if r == nil {
			continue
		}
		out[name] = map[string]bool{}
		for _, alt := range r.Alternatives {
			walkTermsHelper(alt.Terms, out[name])
		}
	}
	return out
}

func walkTermsHelper(terms []grammar.Term, out map[string]bool) {
	for i := range terms {
		t := &terms[i]
		if t.Kind == grammar.TermNonTerminal {
			out[t.Name] = true
		}
		if t.Sub != nil {
			walkTermsHelper(t.Sub.Terms, out)
		}
	}
}

func ruleHashes(g *grammar.Grammar) map[string]string {
	out := make(map[string]string, len(g.Rules))
	for _, r := range g.Rules {
		out[r.Name] = version.CanonicalRule(r)
	}
	return out
}

// lexerRulesEqual 逐字段比较两份词法规则表。
func lexerRulesEqual(a, b []grammar.LexerRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type nilGrammarError struct{}

func (nilGrammarError) Error() string { return "incremental: nil grammar" }

func errNilGrammar() error { return nilGrammarError{} }
