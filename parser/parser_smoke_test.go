package parser

import (
	"testing"

	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/lexer"
)

func buildExprParser(t *testing.T) *Parser {
	t.Helper()
	g := &grammar.Grammar{
		Name: "expr",
		LexerRules: []grammar.LexerRule{
			{Name: "NUM", Pattern: `[0-9]+`},
			{Name: "PLUS", Pattern: `\+`},
			{Name: "WS", Pattern: `\s+`, Ignore: true},
		},
		Rules: []grammar.Rule{
			{Name: "expr", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.NonTerminal("expr"), grammar.Terminal("PLUS"), grammar.NonTerminal("num")),
				grammar.Seq(grammar.NonTerminal("num")),
			}},
			{Name: "num", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.Terminal("NUM")),
			}},
		},
		Start: "expr",
	}
	lx, err := lexer.Compile(g.LexerRules)
	if err != nil {
		t.Fatalf("lexer compile: %v", err)
	}
	p, err := New(g, lx, "expr")
	if err != nil {
		t.Fatalf("parser new: %v", err)
	}
	return p
}

func TestDirectLeftRecursion(t *testing.T) {
	p := buildExprParser(t)
	res := p.Parse("1+2+3", nil)
	if !res.OK() {
		t.Fatalf("expected OK, errors=%v", res.Errors)
	}
	// 左结合：expr(expr(expr(1)+2)+3)
	root := res.Root
	if root.Symbol != "expr" || len(root.Children) != 3 {
		t.Fatalf("bad tree shape: %+v", root)
	}
	inner := root.Children[0]
	if inner.Symbol != "expr" || len(inner.Children) != 3 {
		t.Fatalf("expected left-associative nesting, got %+v", inner)
	}
	innermost := inner.Children[0]
	if innermost.Symbol != "expr" || len(innermost.Children) != 1 || innermost.Children[0].Symbol != "num" {
		t.Fatalf("bad innermost: %+v", innermost)
	}
}

func TestIndirectLeftRecursion(t *testing.T) {
	g := &grammar.Grammar{
		Name: "indirect",
		LexerRules: []grammar.LexerRule{
			{Name: "A", Pattern: `a`},
			{Name: "B", Pattern: `b`},
			{Name: "X", Pattern: `x`},
			{Name: "Y", Pattern: `y`},
		},
		Rules: []grammar.Rule{
			{Name: "S", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.NonTerminal("T"), grammar.Terminal("X")),
				grammar.Seq(grammar.Terminal("A")),
			}},
			{Name: "T", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.NonTerminal("S"), grammar.Terminal("Y")),
				grammar.Seq(grammar.Terminal("B")),
			}},
		},
		Start: "S",
	}
	lx, err := lexer.Compile(g.LexerRules)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(g, lx, "S")
	if err != nil {
		t.Fatal(err)
	}
	// S => T x => S y x => a y x
	res := p.Parse("ayx", nil)
	if !res.OK() {
		t.Fatalf("indirect left recursion failed: %v", res.Errors)
	}
	if res.Consumed != 3 {
		t.Fatalf("consumed=%d want 3", res.Consumed)
	}
}

func TestErrorLocation(t *testing.T) {
	p := buildExprParser(t)
	res := p.Parse("1+\n+2", nil)
	if res.OK() {
		t.Fatal("expected failure")
	}
	if len(res.Errors) != 1 {
		t.Fatalf("want 1 error, got %d: %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Line != 2 || e.Column != 1 {
		t.Fatalf("want line 2 col 1, got %d:%d (%s)", e.Line, e.Column, e)
	}
}
