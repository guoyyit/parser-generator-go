// Package version 提供语法定义的规范化表示、指纹与版本兼容性检查。
//
// 已生成的解析器产物带有语法指纹；当调用方持有的语法定义与产物
// 不一致时，Check 会识别差异并给出明确原因。
package version

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/guoyyit/parser-generator-go/grammar"
)

// Fingerprint 是语法定义的 SHA-256 指纹。
type Fingerprint [32]byte

// String 返回十六进制表示。
func (f Fingerprint) String() string { return hex.EncodeToString(f[:]) }

// Equal 比较两个指纹。
func (f Fingerprint) Equal(o Fingerprint) bool { return f == o }

// FingerprintOf 计算语法定义的指纹。
// 指纹由语法名/起始规则、各词法规则、各产生式的分段哈希 XOR 组合而成，
// 与声明顺序无关；增量校验器可用 HashRule/HashLexerRule 局部更新指纹。
func FingerprintOf(g *grammar.Grammar) Fingerprint {
	if g == nil {
		return Fingerprint{}
	}
	fp := HashPiece("meta", g.Name+"x00"+g.Start)
	for _, lr := range g.LexerRules {
		fp.XOR(HashLexerRule(lr))
	}
	for _, r := range g.Rules {
		fp.XOR(HashRule(r))
	}
	return fp
}

// HashPiece 计算单个规范化片段的哈希。
func HashPiece(kind, content string) Fingerprint {
	return sha256.Sum256([]byte(kind + "x00" + content))
}

// HashLexerRule 计算单条词法规则的分段哈希。
func HashLexerRule(lr grammar.LexerRule) Fingerprint {
	return HashPiece("lex", lr.Name+"x00"+lr.Pattern+"x00"+
		strconv.Itoa(lr.Priority)+"x00"+strconv.FormatBool(lr.Ignore))
}

// HashRule 计算单条产生式的分段哈希。
func HashRule(r grammar.Rule) Fingerprint {
	return HashPiece("rule", r.Name+"x00"+CanonicalRule(r))
}

// XOR 把另一指纹按位异或并入（用于增量更新）。
func (f *Fingerprint) XOR(o Fingerprint) {
	for i := range f {
		f[i] ^= o[i]
	}
}

// Canonical 生成语法定义的规范化字符串表示（与声明顺序无关）。
func Canonical(g *grammar.Grammar) string {
	if g == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString("name=")
	b.WriteString(g.Name)
	b.WriteString("\nstart=")
	b.WriteString(g.Start)
	b.WriteByte('\n')
	lex := append([]grammar.LexerRule(nil), g.LexerRules...)
	sort.Slice(lex, func(i, j int) bool { return lex[i].Name < lex[j].Name })
	for _, lr := range lex {
		b.WriteString("lex ")
		b.WriteString(lr.Name)
		b.WriteString(" prio=")
		b.WriteString(strconv.Itoa(lr.Priority))
		b.WriteString(" ignore=")
		b.WriteString(strconv.FormatBool(lr.Ignore))
		b.WriteString(" pattern=")
		b.WriteString(strconv.Quote(lr.Pattern))
		b.WriteByte('\n')
	}
	rules := append([]grammar.Rule(nil), g.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].Name < rules[j].Name })
	for _, r := range rules {
		b.WriteString("rule ")
		b.WriteString(r.Name)
		b.WriteByte('\n')
		writeRuleCanonical(&b, r)
	}
	return b.String()
}

func writeRuleCanonical(b *strings.Builder, r grammar.Rule) {
	for _, alt := range r.Alternatives {
		b.WriteString("  alt ")
		writeAltCanonical(b, alt)
		b.WriteByte('\n')
	}
}

func writeAltCanonical(b *strings.Builder, a grammar.Alternative) {
	for i, t := range a.Terms {
		if i > 0 {
			b.WriteByte(' ')
		}
		writeTermCanonical(b, t)
	}
}

func writeTermCanonical(b *strings.Builder, t grammar.Term) {
	switch t.Kind {
	case grammar.TermNonTerminal:
		b.WriteString("nt(")
		b.WriteString(t.Name)
		b.WriteByte(')')
	case grammar.TermTerminal:
		b.WriteString("t(")
		b.WriteString(t.Name)
		b.WriteByte(')')
	case grammar.TermOptional:
		b.WriteString("opt(")
		writeAltCanonical(b, *t.Sub)
		b.WriteByte(')')
	case grammar.TermZeroOrMore:
		b.WriteString("star(")
		writeAltCanonical(b, *t.Sub)
		b.WriteByte(')')
	case grammar.TermOneOrMore:
		b.WriteString("plus(")
		writeAltCanonical(b, *t.Sub)
		b.WriteByte(')')
	case grammar.TermGroup:
		b.WriteString("grp(")
		writeAltCanonical(b, *t.Sub)
		b.WriteByte(')')
	}
}

// ChangeKind 标识兼容性差异的种类。
type ChangeKind int

const (
	ChangeLexerAdded ChangeKind = iota
	ChangeLexerRemoved
	ChangeLexerModified
	ChangeRuleAdded
	ChangeRuleRemoved
	ChangeRuleModified
	ChangeStartChanged
	ChangeNameChanged
)

// Change 描述一处语法差异及原因。
type Change struct {
	Kind   ChangeKind
	Symbol string
	Detail string
}

func (c Change) String() string {
	var what string
	switch c.Kind {
	case ChangeLexerAdded:
		what = "lexer rule added"
	case ChangeLexerRemoved:
		what = "lexer rule removed"
	case ChangeLexerModified:
		what = "lexer rule modified"
	case ChangeRuleAdded:
		what = "rule added"
	case ChangeRuleRemoved:
		what = "rule removed"
	case ChangeRuleModified:
		what = "rule modified"
	case ChangeStartChanged:
		what = "start rule changed"
	case ChangeNameChanged:
		what = "grammar name changed"
	}
	if c.Symbol != "" {
		return fmt.Sprintf("%s: %s (%s)", what, c.Symbol, c.Detail)
	}
	return fmt.Sprintf("%s (%s)", what, c.Detail)
}

// Compatibility 是版本兼容性检查的结果。
type Compatibility struct {
	Compatible bool
	Changes    []Change
}

func (c *Compatibility) Error() string {
	if c == nil || c.Compatible {
		return "compatible"
	}
	var b strings.Builder
	b.WriteString("grammar is incompatible with generated artifact:")
	for _, ch := range c.Changes {
		fmt.Fprintf(&b, "\n  - %s", ch)
	}
	return b.String()
}

// Check 比较旧语法（生成产物所依据的版本）与新语法定义，
// 不一致时返回全部差异及原因。
func Check(oldG, newG *grammar.Grammar) *Compatibility {
	c := &Compatibility{Compatible: true}
	if oldG == nil || newG == nil {
		c.Compatible = false
		c.Changes = append(c.Changes, Change{Kind: ChangeNameChanged, Detail: "nil grammar"})
		return c
	}
	add := func(k ChangeKind, sym, detail string) {
		c.Compatible = false
		c.Changes = append(c.Changes, Change{Kind: k, Symbol: sym, Detail: detail})
	}
	if oldG.Name != newG.Name {
		add(ChangeNameChanged, "", fmt.Sprintf("%q -> %q", oldG.Name, newG.Name))
	}
	if oldG.Start != newG.Start {
		add(ChangeStartChanged, "", fmt.Sprintf("%q -> %q", oldG.Start, newG.Start))
	}
	oldLex := lexByName(oldG)
	newLex := lexByName(newG)
	for name, ol := range oldLex {
		nl, ok := newLex[name]
		if !ok {
			add(ChangeLexerRemoved, name, "present in generated artifact, missing in new grammar")
			continue
		}
		if ol != nl {
			add(ChangeLexerModified, name, fmt.Sprintf("pattern %q->%q priority %d->%d ignore %v->%v",
				ol.Pattern, nl.Pattern, ol.Priority, nl.Priority, ol.Ignore, nl.Ignore))
		}
	}
	for name := range newLex {
		if _, ok := oldLex[name]; !ok {
			add(ChangeLexerAdded, name, "not present in generated artifact")
		}
	}
	oldRules := rulesByName(oldG)
	newRules := rulesByName(newG)
	for name, or := range oldRules {
		nr, ok := newRules[name]
		if !ok {
			add(ChangeRuleRemoved, name, "present in generated artifact, missing in new grammar")
			continue
		}
		if CanonicalRule(or) != CanonicalRule(nr) {
			add(ChangeRuleModified, name, "productions differ")
		}
	}
	for name := range newRules {
		if _, ok := oldRules[name]; !ok {
			add(ChangeRuleAdded, name, "not present in generated artifact")
		}
	}
	return c
}

func lexByName(g *grammar.Grammar) map[string]grammar.LexerRule {
	out := map[string]grammar.LexerRule{}
	for _, lr := range g.LexerRules {
		out[lr.Name] = lr
	}
	return out
}

func rulesByName(g *grammar.Grammar) map[string]grammar.Rule {
	out := map[string]grammar.Rule{}
	for _, r := range g.Rules {
		out[r.Name] = r
	}
	return out
}

// CanonicalRule 返回单条产生式的规范化表示（供增量校验做规则级哈希）。
func CanonicalRule(r grammar.Rule) string {
	var b strings.Builder
	writeRuleCanonical(&b, r)
	return b.String()
}
