package generator

import (
	"github.com/guoyyit/parser-generator-go/grammar"
)

// FactIndex 是事实引擎使用的静态结构索引，可跨校验增量复用：
// 局部修改时按规则打补丁，不重建全图。
type FactIndex struct {
	firstDeps     map[string]map[string]bool // 规则 -> 左缘位置引用的规则
	followSites   map[string][]followSite    // 规则 -> 它的全部出现位置
	followCtxDeps map[string]map[string]bool // 规则 -> 其 FOLLOW 方程读取 FIRST/nullable 的规则
	deps          map[string]map[string]bool // 规则 -> 全部引用的规则

	// 反向依赖
	firstRev map[string]map[string]bool
	allRev   map[string]map[string]bool
	fctxRev  map[string]map[string]bool
}

// Deps 返回规则依赖图（只读）。
func (ix *FactIndex) Deps() map[string]map[string]bool { return ix.deps }

// BuildFactIndex 为语法构建全量结构索引。
func BuildFactIndex(g *grammar.Grammar) *FactIndex {
	ix := &FactIndex{
		firstDeps:     map[string]map[string]bool{},
		followSites:   map[string][]followSite{},
		followCtxDeps: map[string]map[string]bool{},
		deps:          map[string]map[string]bool{},
		firstRev:      map[string]map[string]bool{},
		allRev:        map[string]map[string]bool{},
		fctxRev:       map[string]map[string]bool{},
	}
	for i := range g.Rules {
		r := &g.Rules[i]
		ix.firstDeps[r.Name] = nil
		ix.deps[r.Name] = nil
		for ai := range r.Alternatives {
			ix.addRuleAlt(g, r.Name, ai, &r.Alternatives[ai])
		}
	}
	ix.rebuildReverse()
	return ix
}

// addRuleAlt 把一条分支的结构信息加入索引。
func (ix *FactIndex) addRuleAlt(g *grammar.Grammar, rule string, altIdx int, alt *grammar.Alternative) {
	collectEdgeDepsInto(alt.Terms, rule, ix.firstDeps)
	walkTerms(alt.Terms, func(t *grammar.Term) {
		if t.Kind == grammar.TermNonTerminal {
			if ix.deps[rule] == nil {
				ix.deps[rule] = map[string]bool{}
			}
			ix.deps[rule][t.Name] = true
		}
	})
	ix.indexFollowSites(g, rule, altIdx, alt.Terms, nil)
}

func (ix *FactIndex) indexFollowSites(g *grammar.Grammar, rule string, altIdx int, terms []grammar.Term, prefix []int) {
	for i := range terms {
		t := &terms[i]
		path := append(append([]int(nil), prefix...), i)
		if t.Kind == grammar.TermNonTerminal {
			ix.followSites[t.Name] = append(ix.followSites[t.Name], followSite{
				rule: rule, alt: altIdx, path: path,
			})
			ix.addSiteCtxDeps(g, rule, altIdx, path, t.Name)
		}
		if t.Sub != nil {
			ix.indexFollowSites(g, rule, altIdx, t.Sub.Terms, path)
		}
	}
}

// addSiteCtxDeps 把某出现位置的 FOLLOW 上下文依赖并入索引。
func (ix *FactIndex) addSiteCtxDeps(g *grammar.Grammar, rule string, altIdx int, path []int, target string) {
	r := g.FindRule(rule)
	if r == nil {
		return
	}
	terms := r.Alternatives[altIdx].Terms
	for d := 0; d < len(path); d++ {
		idx := path[d]
		ix.addCtxDeps(target, terms[idx+1:])
		t := &terms[idx]
		if d < len(path)-1 {
			if t.Kind == grammar.TermZeroOrMore || t.Kind == grammar.TermOneOrMore {
				ix.addCtxDeps(target, t.Sub.Terms)
			}
			terms = t.Sub.Terms
		}
	}
}

func (ix *FactIndex) addCtxDeps(target string, terms []grammar.Term) {
	walkTerms(terms, func(t *grammar.Term) {
		if t.Kind != grammar.TermNonTerminal {
			return
		}
		if ix.followCtxDeps[target] == nil {
			ix.followCtxDeps[target] = map[string]bool{}
		}
		ix.followCtxDeps[target][t.Name] = true
	})
}

func (ix *FactIndex) rebuildReverse() {
	ix.firstRev = map[string]map[string]bool{}
	ix.allRev = map[string]map[string]bool{}
	ix.fctxRev = map[string]map[string]bool{}
	addReverse := func(m map[string]map[string]bool, fwd map[string]map[string]bool) {
		for from, deps := range fwd {
			for d := range deps {
				if m[d] == nil {
					m[d] = map[string]bool{}
				}
				m[d][from] = true
			}
		}
	}
	addReverse(ix.firstRev, ix.firstDeps)
	addReverse(ix.allRev, ix.deps)
	addReverse(ix.fctxRev, ix.followCtxDeps)
}

// Patch 把 changed 集合中的规则按新语法增量更新进索引。
// oldG 提供被修改/删除规则的旧内容（用于摘除旧索引项）。
func (ix *FactIndex) Patch(newG, oldG *grammar.Grammar, changed map[string]bool) {
	affectedX := map[string]bool{} // 出现位置受影响的非终结符
	for name := range changed {
		// 摘除旧的正向边与反向边。
		for d := range ix.firstDeps[name] {
			delete(ix.firstRev[d], name)
		}
		for d := range ix.deps[name] {
			delete(ix.allRev[d], name)
		}
		// 摘除旧的出现位置与 ctx 依赖。
		if old := findRule(oldG, name); old != nil {
			for x := range nonTermOccurrences(old.Alternatives) {
				affectedX[x] = true
				sites := ix.followSites[x]
				kept := sites[:0]
				for _, st := range sites {
					if st.rule != name {
						kept = append(kept, st)
					}
				}
				ix.followSites[x] = kept
			}
		}
		for d := range ix.followCtxDeps[name] {
			delete(ix.fctxRev[d], name)
		}
		delete(ix.followCtxDeps, name)
	}
	for name := range changed {
		// 加入新内容。
		nr := findRule(newG, name)
		if nr == nil {
			delete(ix.firstDeps, name)
			delete(ix.deps, name)
			continue
		}
		ix.firstDeps[name] = nil
		ix.deps[name] = nil
		for ai := range nr.Alternatives {
			ix.addRuleAlt(newG, name, ai, &nr.Alternatives[ai])
		}
		for x := range nonTermOccurrences(nr.Alternatives) {
			affectedX[x] = true
		}
		for d := range ix.firstDeps[name] {
			if ix.firstRev[d] == nil {
				ix.firstRev[d] = map[string]bool{}
			}
			ix.firstRev[d][name] = true
		}
		for d := range ix.deps[name] {
			if ix.allRev[d] == nil {
				ix.allRev[d] = map[string]bool{}
			}
			ix.allRev[d][name] = true
		}
	}
	// 受影响非终结符的 ctx 依赖按现存出现位置重建。
	for x := range affectedX {
		for d := range ix.followCtxDeps[x] {
			delete(ix.fctxRev[d], x)
		}
		newCtx := map[string]bool{}
		for _, site := range ix.followSites[x] {
			r := findRule(newG, site.rule)
			if r == nil || site.alt >= len(r.Alternatives) {
				continue
			}
			terms := r.Alternatives[site.alt].Terms
			for d := 0; d < len(site.path); d++ {
				idx := site.path[d]
				if idx >= len(terms) {
					break
				}
				collectNonTermsInto(terms[idx+1:], newCtx)
				t := &terms[idx]
				if d < len(site.path)-1 {
					if t.Kind == grammar.TermZeroOrMore || t.Kind == grammar.TermOneOrMore {
						collectNonTermsInto(t.Sub.Terms, newCtx)
					}
					terms = t.Sub.Terms
				}
			}
		}
		ix.followCtxDeps[x] = newCtx
		for d := range newCtx {
			if ix.fctxRev[d] == nil {
				ix.fctxRev[d] = map[string]bool{}
			}
			ix.fctxRev[d][x] = true
		}
	}
}

func findRule(g *grammar.Grammar, name string) *grammar.Rule {
	if g == nil {
		return nil
	}
	return g.FindRule(name)
}

// nonTermOccurrences 返回规则所有分支中出现过的非终结符名字集合。
func nonTermOccurrences(alts []grammar.Alternative) map[string]bool {
	out := map[string]bool{}
	for _, alt := range alts {
		walkTerms(alt.Terms, func(t *grammar.Term) {
			if t.Kind == grammar.TermNonTerminal {
				out[t.Name] = true
			}
		})
	}
	return out
}

func collectNonTermsInto(terms []grammar.Term, out map[string]bool) {
	walkTerms(terms, func(t *grammar.Term) {
		if t.Kind == grammar.TermNonTerminal {
			out[t.Name] = true
		}
	})
}

// collectEdgeDepsInto 静态收集序列左缘可能出现的非终结符。
func collectEdgeDepsInto(terms []grammar.Term, rule string, index map[string]map[string]bool) {
	for i := range terms {
		t := &terms[i]
		switch t.Kind {
		case grammar.TermTerminal:
			return
		case grammar.TermNonTerminal:
			if index[rule] == nil {
				index[rule] = map[string]bool{}
			}
			index[rule][t.Name] = true
		case grammar.TermOptional, grammar.TermZeroOrMore, grammar.TermOneOrMore, grammar.TermGroup:
			collectEdgeDepsInto(t.Sub.Terms, rule, index)
		}
	}
}
