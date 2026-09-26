// Package grammar 定义解析器生成器所使用的声明式语法描述模型。
//
// 调用方通过 LexerRule 描述词法规则，通过 Rule/Alternative/Term 描述产生式，
// 由 generator 包完成校验、冲突检测与解析器生成。
package grammar

// Grammar 是一份完整的语法定义：词法规则 + 产生式 + 起始规则。
type Grammar struct {
	Name       string
	LexerRules []LexerRule
	Rules      []Rule
	Start      string
}

// LexerRule 描述一条词法规则。
//
// Pattern 为正则表达式（Go regexp 语法），匹配时遵循：
//   - 最长匹配优先；
//   - 长度相同则 Priority 数值大者胜出；
//   - 长度与优先级都相同属于冲突，会在生成期被拒绝。
//
// Ignore 为 true 的规则（如空白、注释）会被词法层直接丢弃，不进入解析流。
type LexerRule struct {
	Name     string
	Pattern  string
	Priority int
	Ignore   bool
}

// Rule 是一条产生式：Name := Alternatives 中的任意一条。
type Rule struct {
	Name         string
	Alternatives []Alternative
}

// Alternative 是产生式的一个候选分支，由若干 Term 顺序组成。
// 空 Terms 表示空产生式（epsilon）。
type Alternative struct {
	Terms []Term
}

// TermKind 标识语法符号的种类。
type TermKind int

const (
	// TermNonTerminal 引用另一条产生式。
	TermNonTerminal TermKind = iota
	// TermTerminal 引用一个词法单元（LexerRule.Name）。
	TermTerminal
	// TermOptional 表示 0 次或 1 次出现。
	TermOptional
	// TermZeroOrMore 表示 0 次或多次出现。
	TermZeroOrMore
	// TermOneOrMore 表示 1 次或多次出现。
	TermOneOrMore
	// TermGroup 表示括号分组，子表达式本身是一个 Alternative。
	TermGroup
)

// Term 是产生式分支中的一个符号或组合子。
type Term struct {
	Kind TermKind
	// Name 在 Kind 为 TermNonTerminal / TermTerminal 时有效。
	Name string
	// Sub 在 Kind 为 Optional/ZeroOrMore/OneOrMore/Group 时有效。
	Sub *Alternative
}

// NonTerminal 构造一个非终结符引用。
func NonTerminal(name string) Term { return Term{Kind: TermNonTerminal, Name: name} }

// Terminal 构造一个终结符引用（词法单元名）。
func Terminal(name string) Term { return Term{Kind: TermTerminal, Name: name} }

// Optional 构造可选结构。
func Optional(sub Alternative) Term { return Term{Kind: TermOptional, Sub: &sub} }

// ZeroOrMore 构造 0 次或多次重复。
func ZeroOrMore(sub Alternative) Term { return Term{Kind: TermZeroOrMore, Sub: &sub} }

// OneOrMore 构造 1 次或多次重复。
func OneOrMore(sub Alternative) Term { return Term{Kind: TermOneOrMore, Sub: &sub} }

// Group 构造分组。
func Group(sub Alternative) Term { return Term{Kind: TermGroup, Sub: &sub} }

// Seq 用若干 Term 构造一个顺序分支，便于书写产生式。
func Seq(terms ...Term) Alternative { return Alternative{Terms: terms} }

// Epsilon 构造空分支。
func Epsilon() Alternative { return Alternative{} }

// FindRule 按名字查找产生式，未找到返回 nil。
func (g *Grammar) FindRule(name string) *Rule {
	for i := range g.Rules {
		if g.Rules[i].Name == name {
			return &g.Rules[i]
		}
	}
	return nil
}

// FindLexerRule 按名字查找词法规则，未找到返回 nil。
func (g *Grammar) FindLexerRule(name string) *LexerRule {
	for i := range g.LexerRules {
		if g.LexerRules[i].Name == name {
			return &g.LexerRules[i]
		}
	}
	return nil
}

// Clone 深拷贝语法定义，保证调用方与生成器之间不共享可变状态。
func (g *Grammar) Clone() *Grammar {
	if g == nil {
		return nil
	}
	out := &Grammar{
		Name:       g.Name,
		Start:      g.Start,
		LexerRules: append([]LexerRule(nil), g.LexerRules...),
		Rules:      make([]Rule, len(g.Rules)),
	}
	for i, r := range g.Rules {
		out.Rules[i] = r.Clone()
	}
	return out
}

// Clone 深拷贝一条产生式。
func (r Rule) Clone() Rule {
	out := Rule{Name: r.Name, Alternatives: make([]Alternative, len(r.Alternatives))}
	for i, a := range r.Alternatives {
		out.Alternatives[i] = a.Clone()
	}
	return out
}

// Clone 深拷贝一个分支。
func (a Alternative) Clone() Alternative {
	out := Alternative{Terms: make([]Term, len(a.Terms))}
	for i, t := range a.Terms {
		out.Terms[i] = t.Clone()
	}
	return out
}

// Clone 深拷贝一个 Term（含子表达式）。
func (t Term) Clone() Term {
	out := t
	if t.Sub != nil {
		sub := t.Sub.Clone()
		out.Sub = &sub
	}
	return out
}
