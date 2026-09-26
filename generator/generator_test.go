package generator

import (
	"strings"
	"testing"

	"github.com/guoyyit/parser-generator-go/grammar"
)

func exprGrammar() *grammar.Grammar {
	return &grammar.Grammar{
		Name: "expr",
		LexerRules: []grammar.LexerRule{
			{Name: "NUM", Pattern: `[0-9]+`},
			{Name: "PLUS", Pattern: `\+`},
			{Name: "STAR", Pattern: `\*`},
			{Name: "WS", Pattern: `\s+`, Ignore: true},
		},
		Rules: []grammar.Rule{
			{Name: "expr", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.NonTerminal("expr"), grammar.Terminal("PLUS"), grammar.NonTerminal("term")),
				grammar.Seq(grammar.NonTerminal("term")),
			}},
			{Name: "term", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.NonTerminal("term"), grammar.Terminal("STAR"), grammar.NonTerminal("atom")),
				grammar.Seq(grammar.NonTerminal("atom")),
			}},
			{Name: "atom", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.Terminal("NUM")),
			}},
		},
		Start: "expr",
	}
}

func TestGenerateExprGrammar(t *testing.T) {
	gen := NewGenerator()
	art, rep, err := gen.Generate(exprGrammar())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("unexpected diagnostics: %v", rep)
	}
	res := art.Parse("1+2*3+4", nil)
	if !res.OK() {
		t.Fatalf("parse failed: %v", res.Errors)
	}
}

func TestAmbiguousExpressionRejected(t *testing.T) {
	g := exprGrammar()
	// E = E "+" E | NUM —— 经典歧义文法，必须被拒绝。
	g.Rules = []grammar.Rule{
		{Name: "e", Alternatives: []grammar.Alternative{
			grammar.Seq(grammar.NonTerminal("e"), grammar.Terminal("PLUS"), grammar.NonTerminal("e")),
			grammar.Seq(grammar.Terminal("NUM")),
		}},
	}
	g.Start = "e"
	art, rep, err := NewGenerator().Generate(g)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if art != nil {
		t.Fatal("ambiguous grammar must not produce an artifact")
	}
	if rep.OK() {
		t.Fatal("expected conflict report")
	}
	found := false
	for _, d := range rep.Diagnostics {
		if strings.Contains(d.Msg, "precedence level") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tail-reach diagnostic, got: %v", rep)
	}
}

func TestFirstFirstConflictRejected(t *testing.T) {
	g := exprGrammar()
	g.Rules = []grammar.Rule{
		{Name: "s", Alternatives: []grammar.Alternative{
			grammar.Seq(grammar.Terminal("NUM"), grammar.Terminal("PLUS")),
			grammar.Seq(grammar.Terminal("NUM"), grammar.Terminal("STAR")),
		}},
	}
	g.Start = "s"
	art, rep, _ := NewGenerator().Generate(g)
	if art != nil || rep.OK() {
		t.Fatalf("expected FIRST/FIRST rejection, art=%v rep=%v", art != nil, rep)
	}
}

func TestDuplicateDeclarationsRejected(t *testing.T) {
	g := exprGrammar()
	g.Rules = append(g.Rules, grammar.Rule{
		Name:         "expr",
		Alternatives: []grammar.Alternative{grammar.Seq(grammar.Terminal("NUM"))},
	})
	_, rep, _ := NewGenerator().Generate(g)
	if rep.OK() {
		t.Fatal("duplicate rule must be rejected")
	}

	g2 := exprGrammar()
	g2.LexerRules = append(g2.LexerRules, grammar.LexerRule{Name: "NUM", Pattern: `[0-9]+`})
	_, rep2, _ := NewGenerator().Generate(g2)
	if rep2.OK() {
		t.Fatal("duplicate lexer rule must be rejected")
	}
}

func TestUndefinedReferenceRejected(t *testing.T) {
	g := exprGrammar()
	g.Rules[0].Alternatives[0].Terms[2] = grammar.NonTerminal("missing")
	_, rep, _ := NewGenerator().Generate(g)
	if rep.OK() {
		t.Fatal("undefined reference must be rejected")
	}
}

func TestOptionalRepeatGroup(t *testing.T) {
	g := &grammar.Grammar{
		Name: "list",
		LexerRules: []grammar.LexerRule{
			{Name: "NUM", Pattern: `[0-9]+`},
			{Name: "COMMA", Pattern: `,`},
			{Name: "SEMI", Pattern: `;`},
			{Name: "WS", Pattern: `\s+`, Ignore: true},
		},
		Rules: []grammar.Rule{
			{Name: "list", Alternatives: []grammar.Alternative{
				grammar.Seq(
					grammar.Terminal("NUM"),
					grammar.ZeroOrMore(grammar.Seq(grammar.Terminal("COMMA"), grammar.Terminal("NUM"))),
					grammar.Optional(grammar.Seq(grammar.Terminal("SEMI"))),
				),
			}},
		},
		Start: "list",
	}
	art, rep, err := NewGenerator().Generate(g)
	if err != nil || !rep.OK() {
		t.Fatalf("generate: %v %v", err, rep)
	}
	res := art.Parse("1, 2, 3;", nil)
	if !res.OK() {
		t.Fatalf("parse failed: %v", res.Errors)
	}
	if res2 := art.Parse("7", nil); !res2.OK() {
		t.Fatalf("single element failed: %v", res2.Errors)
	}
}

func TestHiddenLeftRecursionRejected(t *testing.T) {
	// s = opt s X | X，opt 可推出非空串 "y" —— 递归符号前有非空前缀，
	// 贪婪运行期无法确定解析，必须明确拒绝。
	g := &grammar.Grammar{
		Name:  "hidden",
		Start: "s",
		LexerRules: []grammar.LexerRule{
			{Name: "X", Pattern: `x`},
			{Name: "Y", Pattern: `y`},
		},
		Rules: []grammar.Rule{
			{Name: "s", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.NonTerminal("opt"), grammar.NonTerminal("s"), grammar.Terminal("X")),
				grammar.Seq(grammar.Terminal("X")),
			}},
			{Name: "opt", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.Terminal("Y")),
				grammar.Epsilon(),
			}},
		},
	}
	art, rep, _ := NewGenerator().Generate(g)
	if art != nil || rep.OK() {
		t.Fatalf("hidden left recursion behind non-empty prefix must be rejected: %v", rep)
	}
	found := false
	for _, d := range rep.Diagnostics {
		if strings.Contains(d.Msg, "non-empty prefix") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected non-empty prefix diagnostic, got: %v", rep)
	}
}

func TestNullablePrefixLeftRecursionAllowed(t *testing.T) {
	// opt 只能取 epsilon：前缀不可推出非空串，允许。
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
	art, rep, _ := NewGenerator().Generate(g)
	if !rep.OK() || art == nil {
		t.Fatalf("epsilon-only prefix left recursion must be allowed: %v", rep)
	}
	if r := art.Parse("x x", nil); !r.OK() {
		t.Fatalf("parse failed: %v", r.Errors)
	}
}
