package analysis

import (
	"strings"
	"testing"

	"github.com/guoyyit/parser-generator-go/grammar"
)

func arithmeticGrammar(t *testing.T, ambiguous bool) *grammar.Grammar {
	t.Helper()
	rec := grammar.Seq(grammar.NT("expr"), grammar.T("PLUS"),
		grammar.T("NUM"))
	if ambiguous {
		rec = grammar.Seq(grammar.NT("expr"), grammar.T("PLUS"),
			grammar.NT("expr"))
	}
	g, err := grammar.NewBuilder("calc", "1.0.0").
		Token("NUM", `[0-9]+`).
		Token("PLUS", `\+`).
		Rule("expr",
			grammar.Seq(grammar.T("NUM")),
			rec,
		).
		Start("expr")
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func build(t *testing.T, g *grammar.Grammar) *NormalGrammar {
	t.Helper()
	ng, err := Normalize(g)
	if err != nil {
		t.Fatal(err)
	}
	return ng
}

func TestNormalizeSugar(t *testing.T) {
	g, err := grammar.NewBuilder("g", "1.0.0").
		Token("A", `a`).
		Rule("s",
			grammar.Seq(grammar.Opt(grammar.Seq(grammar.T("A")))),
			grammar.Seq(grammar.Star(grammar.Seq(grammar.T("A")))),
			grammar.Seq(grammar.Plus(grammar.Seq(grammar.T("A")))),
		).
		Start("s")
	if err != nil {
		t.Fatal(err)
	}
	ng := build(t, g)
	synth := 0
	for _, r := range ng.Rules {
		if r.Synthetic {
			synth++
		}
	}
	if synth < 3 {
		t.Fatalf("want >=3 synthetic rules, got %d", synth)
	}
}

func TestFirstFollow(t *testing.T) {
	ng := build(t, arithmeticGrammar(t, false))
	first := ng.ComputeFirst()
	follow := ng.ComputeFollow()
	if !first["expr"]["NUM"] {
		t.Fatalf("FIRST(expr) must contain NUM: %v", first["expr"])
	}
	if !follow["expr"][EOF] || !follow["expr"]["PLUS"] {
		t.Fatalf("FOLLOW(expr) must contain EOF and PLUS: %v", follow["expr"])
	}
}

func TestAmbiguityDetected(t *testing.T) {
	ng := build(t, arithmeticGrammar(t, true))
	c := ng.BoundedAmbiguity()
	if c == nil {
		t.Fatal("ambiguous grammar was accepted silently")
	}
	if !strings.Contains(c.Message, "distinct parse derivations") {
		t.Fatalf("unexpected message: %v", c.Message)
	}
}

func TestUnambiguousLeftRecursionAccepted(t *testing.T) {
	ng := build(t, arithmeticGrammar(t, false))
	if c := ng.BoundedAmbiguity(); c != nil {
		t.Fatalf("left-recursive but unambiguous grammar rejected: %v", c)
	}
	if len(ng.Conflicts()) != 0 {
		t.Fatalf("unexpected conflicts: %v", ng.Conflicts())
	}
}

func TestLL1FirstConflict(t *testing.T) {
	g, err := grammar.NewBuilder("g", "1.0.0").
		Token("A", `a`).
		Rule("s",
			grammar.Seq(grammar.T("A")),
			grammar.Seq(grammar.T("A")),
		).
		Start("s")
	if err != nil {
		t.Fatal(err)
	}
	ng := build(t, g)
	if len(ng.Conflicts()) == 0 && ng.BoundedAmbiguity() == nil {
		t.Fatal("duplicate alternatives must be reported")
	}
}

func TestNonConsumingRecursion(t *testing.T) {
	g, err := grammar.NewBuilder("g", "1.0.0").
		Token("A", `a`).
		Rule("s",
			grammar.Seq(grammar.NT("s")),
			grammar.Seq(grammar.T("A")),
		).
		Start("s")
	if err != nil {
		t.Fatal(err)
	}
	ng := build(t, g)
	// A -> A yields infinitely many derivations; the bounded detector flags it.
	if c := ng.BoundedAmbiguity(); c == nil {
		t.Fatal("non-consuming recursion must be flagged")
	}
}
