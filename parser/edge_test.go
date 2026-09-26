package parser

import (
	"testing"

	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/lexer"
)

func buildParser(t *testing.T, g *grammar.Grammar) *Parser {
	t.Helper()
	lx, err := lexer.Compile(g.LexerRules)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(g, lx, g.Start)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNullableAndEmptyInput(t *testing.T) {
	g := &grammar.Grammar{
		Name:  "opt",
		Start: "s",
		LexerRules: []grammar.LexerRule{
			{Name: "A", Pattern: `a`},
			{Name: "WS", Pattern: `\s+`, Ignore: true},
		},
		Rules: []grammar.Rule{
			{Name: "s", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.Optional(grammar.Seq(grammar.Terminal("A")))),
			}},
		},
	}
	p := buildParser(t, g)
	if r := p.Parse("", nil); !r.OK() {
		t.Fatalf("empty input should parse via optional: %v", r.Errors)
	}
	if r := p.Parse("a", nil); !r.OK() {
		t.Fatalf("single a: %v", r.Errors)
	}
}

func TestGroupAndOneOrMore(t *testing.T) {
	g := &grammar.Grammar{
		Name:  "grp",
		Start: "s",
		LexerRules: []grammar.LexerRule{
			{Name: "A", Pattern: `a`},
			{Name: "B", Pattern: `b`},
			{Name: "WS", Pattern: `\s+`, Ignore: true},
		},
		Rules: []grammar.Rule{
			{Name: "s", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.OneOrMore(grammar.Seq(grammar.Terminal("A"), grammar.Terminal("B")))),
			}},
		},
	}
	p := buildParser(t, g)
	r := p.Parse("ab ab ab", nil)
	if !r.OK() {
		t.Fatalf("groups/oneormore: %v", r.Errors)
	}
	if r := p.Parse("", nil); r.OK() {
		t.Fatal("one-or-more must reject empty input")
	}
}

func TestLeftRecursionThroughNullablePrefix(t *testing.T) {
	// s = opt s "x" | "x"；opt 只能取 epsilon（前缀不可推出非空串，
	// 生成器允许此形态；可推出非空串的前缀会被生成器拒绝，见 generator 测试）。
	g := &grammar.Grammar{
		Name:  "hidden",
		Start: "s",
		LexerRules: []grammar.LexerRule{
			{Name: "X", Pattern: `x`},
			{Name: "WS", Pattern: `\s+`, Ignore: true},
		},
		Rules: []grammar.Rule{
			{Name: "s", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.NonTerminal("opt"), grammar.NonTerminal("s"), grammar.Terminal("X")),
				grammar.Seq(grammar.Terminal("X")),
			}},
			{Name: "opt", Alternatives: []grammar.Alternative{
				grammar.Epsilon(),
			}},
		},
	}
	p := buildParser(t, g)
	// opt 取 epsilon：s 经可空前缀递归到自身，"x x" 生长成功。
	if r := p.Parse("x x", nil); !r.OK() {
		t.Fatalf("nullable-prefix left recursion (eps opt): %v", r.Errors)
	}
}
