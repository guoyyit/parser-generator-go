package incremental

import (
	"fmt"
	"testing"
	"time"

	"github.com/guoyyit/parser-generator-go/generator"
	"github.com/guoyyit/parser-generator-go/grammar"
)

// bigGrammar 生成有 n 个独立分支规则的大语法：
// s -> "T0" r0 | "T1" r1 | ...；ri -> "Ti"。
// 局部修改只影响修改的规则与起始规则。
func bigGrammar(n int) *grammar.Grammar {
	g := &grammar.Grammar{Name: "big", Start: "s"}
	for i := 0; i < n; i++ {
		g.LexerRules = append(g.LexerRules, grammar.LexerRule{
			Name: fmt.Sprintf("T%d", i), Pattern: fmt.Sprintf("tok%d", i),
		})
	}
	g.LexerRules = append(g.LexerRules, grammar.LexerRule{Name: "WS", Pattern: `\s+`, Ignore: true})
	s := grammar.Rule{Name: "s"}
	for i := 0; i < n; i++ {
		tok := fmt.Sprintf("T%d", i)
		s.Alternatives = append(s.Alternatives,
			grammar.Seq(grammar.Terminal(tok), grammar.NonTerminal(fmt.Sprintf("r%d", i))))
		g.Rules = append(g.Rules, grammar.Rule{
			Name:         fmt.Sprintf("r%d", i),
			Alternatives: []grammar.Alternative{grammar.Seq(grammar.Terminal(tok))},
		})
	}
	g.Rules = append([]grammar.Rule{s}, g.Rules...)
	return g
}

// chainGrammar 生成深链语法：s -> r0, ri -> "Ti" r(i+1)。
// 用于检验 FOLLOW 沿链传播时增量结果与全量一致。
func chainGrammar(n int) *grammar.Grammar {
	g := &grammar.Grammar{Name: "chain", Start: "s"}
	for i := 0; i < n; i++ {
		g.LexerRules = append(g.LexerRules, grammar.LexerRule{
			Name: fmt.Sprintf("T%d", i), Pattern: fmt.Sprintf("tok%d", i),
		})
	}
	g.Rules = append(g.Rules, grammar.Rule{
		Name:         "s",
		Alternatives: []grammar.Alternative{grammar.Seq(grammar.NonTerminal("r0"))},
	})
	for i := 0; i < n; i++ {
		terms := []grammar.Term{grammar.Terminal(fmt.Sprintf("T%d", i))}
		if i+1 < n {
			terms = append(terms, grammar.NonTerminal(fmt.Sprintf("r%d", i+1)))
		}
		g.Rules = append(g.Rules, grammar.Rule{
			Name:         fmt.Sprintf("r%d", i),
			Alternatives: []grammar.Alternative{grammar.Seq(terms...)},
		})
	}
	return g
}

func TestIncrementalNoOp(t *testing.T) {
	g := bigGrammar(50)
	c, rep, err := NewChecker(g)
	if err != nil || !rep.OK() {
		t.Fatalf("init: %v %v", err, rep)
	}
	rep2, err := c.Update(g.Clone())
	if err != nil {
		t.Fatal(err)
	}
	if rep2 != rep {
		t.Fatal("no-op update should return cached report")
	}
}

func TestIncrementalLeafChangeMatchesFull(t *testing.T) {
	g := bigGrammar(60)
	c, rep, err := NewChecker(g)
	if err != nil || !rep.OK() {
		t.Fatalf("init: %v %v", err, rep)
	}
	// 修改叶子规则 r59：引入一个 FIRST/FIRST 冲突。
	g2 := g.Clone()
	g2.Rules[len(g2.Rules)-1].Alternatives = append(g2.Rules[len(g2.Rules)-1].Alternatives,
		grammar.Seq(grammar.Terminal("T59")))
	rep2, err := c.Update(g2)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.OK() {
		t.Fatal("expected conflict in modified rule")
	}
	// 与全量重分析对比：诊断集合必须一致。
	_, fullRep := generator.Analyze(g2)
	if len(rep2.Diagnostics) != len(fullRep.Diagnostics) {
		t.Fatalf("incremental=%d diags, full=%d\ninc: %v\nfull: %v",
			len(rep2.Diagnostics), len(fullRep.Diagnostics), rep2, fullRep)
	}
	// 影响集应当很小（远小于规则总数）。
	if got := len(c.LastAffected()); got > 5 {
		t.Fatalf("affected set too large: %d", got)
	}
}

func TestIncrementalChainMatchesFull(t *testing.T) {
	// 链式语法：FOLLOW 会沿链传播，验证增量结果仍与全量一致。
	g := chainGrammar(80)
	c, rep, err := NewChecker(g)
	if err != nil || !rep.OK() {
		t.Fatalf("init: %v %v", err, rep)
	}
	g2 := g.Clone()
	// 叶子规则改为可空分支 + 原分支，影响整条链的 FOLLOW/nullable。
	g2.Rules[len(g2.Rules)-1].Alternatives = append(g2.Rules[len(g2.Rules)-1].Alternatives,
		grammar.Epsilon())
	rep2, err := c.Update(g2)
	if err != nil {
		t.Fatal(err)
	}
	_, fullRep := generator.Analyze(g2)
	if rep2.OK() != fullRep.OK() {
		t.Fatalf("incremental OK=%v, full OK=%v\ninc: %v\nfull: %v",
			rep2.OK(), fullRep.OK(), rep2, fullRep)
	}
	if len(rep2.Diagnostics) != len(fullRep.Diagnostics) {
		t.Fatalf("incremental=%d diags, full=%d\ninc: %v\nfull: %v",
			len(rep2.Diagnostics), len(fullRep.Diagnostics), rep2, fullRep)
	}
}

func TestIncrementalPerformance(t *testing.T) {
	const n = 800
	const rounds = 20
	g := bigGrammar(n)
	c, _, err := NewChecker(g)
	if err != nil {
		t.Fatal(err)
	}
	// 全量重建耗时基准（含校验与生成）。
	start := time.Now()
	for i := 0; i < rounds; i++ {
		if _, _, err := NewChecker(g); err != nil {
			t.Fatal(err)
		}
	}
	full := time.Since(start) / rounds

	// 局部修改：只改一个叶子规则，多次更新取均值。
	g2 := g.Clone()
	g2.Rules[len(g2.Rules)-1].Alternatives[0].Terms[0] = grammar.Terminal("T0")
	start = time.Now()
	for i := 0; i < rounds; i++ {
		if _, err := c.Update(g2); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Update(g); err != nil { // 改回，构成真实的交替更新
			t.Fatal(err)
		}
	}
	inc := time.Since(start) / (rounds * 2)
	t.Logf("full=%v incremental=%v speedup=%.1fx", full, inc, float64(full)/float64(inc))
	if inc*3 > full {
		t.Fatalf("incremental update not significantly faster: full=%v inc=%v", full, inc)
	}
	if got := len(c.LastAffected()); got >= n/4 {
		t.Fatalf("affected set too large: %d of %d", got, n)
	}
}

func TestIncrementalArtifactRegenerated(t *testing.T) {
	g := bigGrammar(5)
	c, rep, err := NewChecker(g)
	if err != nil || !rep.OK() {
		t.Fatalf("init: %v %v", err, rep)
	}
	if c.Artifact() == nil {
		t.Fatal("expected artifact")
	}
	g2 := g.Clone()
	// r4 原为 "T4"，改为 "T4" "T0"。
	g2.Rules[len(g2.Rules)-1].Alternatives[0].Terms = append(
		g2.Rules[len(g2.Rules)-1].Alternatives[0].Terms, grammar.Terminal("T0"))
	rep2, err := c.Update(g2)
	if err != nil || !rep2.OK() {
		t.Fatalf("update: %v %v", err, rep2)
	}
	art := c.Artifact()
	if art == nil {
		t.Fatal("expected regenerated artifact")
	}
	res := art.Parse("tok4 tok4 tok0", nil)
	if !res.OK() {
		t.Fatalf("parse with regenerated artifact failed: %v", res.Errors)
	}
	// 指纹应随语法变化。
	old := bigGrammar(5)
	c2, _, _ := NewChecker(old)
	if c2.Artifact().Fingerprint() == art.Fingerprint() {
		t.Fatal("fingerprint should change after grammar update")
	}
}

func TestIncrementalRuleRemovalAndAdd(t *testing.T) {
	g := bigGrammar(10)
	c, rep, err := NewChecker(g)
	if err != nil || !rep.OK() {
		t.Fatalf("init: %v %v", err, rep)
	}
	// 删除 r5：s 仍然引用它 -> 结构性错误。
	g2 := g.Clone()
	g2.Rules = g2.Rules[:len(g2.Rules)-1] // 去掉最后一条规则 r9，不影响 s？s 引用 r0..r9
	_ = g2
	// 精确删除 r5：重建规则切片。
	g3 := g.Clone()
	var kept []grammar.Rule
	for _, r := range g3.Rules {
		if r.Name != "r5" {
			kept = append(kept, r)
		}
	}
	g3.Rules = kept
	rep3, err := c.Update(g3)
	if err != nil {
		t.Fatal(err)
	}
	if rep3.OK() {
		t.Fatal("removing a referenced rule must fail validation")
	}
	found := false
	for _, d := range rep3.Diagnostics {
		if d.Rule == "s" {
			found = true
		}
	}
	if !found {
		t.Fatal("dangling reference diagnostic should point at using rule s")
	}
	// 加回 r5：恢复正常。
	rep4, err := c.Update(g)
	if err != nil || !rep4.OK() {
		t.Fatalf("restore should validate: %v %v", err, rep4)
	}
	if c.Artifact() == nil {
		t.Fatal("artifact should be regenerated after restore")
	}
}

func TestIncrementalAddedRuleConflict(t *testing.T) {
	g := bigGrammar(10)
	c, _, err := NewChecker(g)
	if err != nil {
		t.Fatal(err)
	}
	// 新增一条规则（不可达），且自身有 FIRST/FIRST 冲突。
	g2 := g.Clone()
	g2.Rules = append(g2.Rules, grammar.Rule{
		Name: "newb",
		Alternatives: []grammar.Alternative{
			grammar.Seq(grammar.Terminal("T0")),
			grammar.Seq(grammar.Terminal("T0"), grammar.Terminal("T1")),
		},
	})
	rep, err := c.Update(g2)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() {
		t.Fatal("newly added rule conflicts must be detected incrementally")
	}
}
