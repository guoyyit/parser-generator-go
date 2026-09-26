package grammar

import "testing"

func TestCloneIsolation(t *testing.T) {
	g := &Grammar{
		Name:       "g",
		Start:      "s",
		LexerRules: []LexerRule{{Name: "A", Pattern: `a`}},
		Rules: []Rule{
			{Name: "s", Alternatives: []Alternative{
				Seq(Terminal("A"), ZeroOrMore(Seq(Terminal("A")))),
			}},
		},
	}
	cp := g.Clone()
	cp.Name = "changed"
	cp.Rules[0].Alternatives[0].Terms[0].Name = "B"
	cp.Rules[0].Alternatives[0].Terms[1].Sub.Terms[0].Name = "C"
	if g.Name != "g" || g.Rules[0].Alternatives[0].Terms[0].Name != "A" {
		t.Fatal("top-level mutation leaked into original")
	}
	sub := g.Rules[0].Alternatives[0].Terms[1].Sub
	if sub.Terms[0].Name != "A" {
		t.Fatalf("nested sub-expression mutation leaked: %v", sub.Terms[0].Name)
	}
}

func TestFinders(t *testing.T) {
	g := &Grammar{
		LexerRules: []LexerRule{{Name: "A", Pattern: `a`}},
		Rules:      []Rule{{Name: "s", Alternatives: []Alternative{Epsilon()}}},
	}
	if g.FindRule("x") != nil || g.FindLexerRule("x") != nil {
		t.Fatal("missing rule lookup must return nil")
	}
	if g.FindRule("s") == nil || g.FindLexerRule("A") == nil {
		t.Fatal("existing rule lookup failed")
	}
}

func TestBuilders(t *testing.T) {
	terms := []Term{
		NonTerminal("n"),
		Terminal("t"),
		Optional(Seq(Terminal("t"))),
		ZeroOrMore(Seq(Terminal("t"))),
		OneOrMore(Seq(Terminal("t"))),
		Group(Seq(Terminal("t"))),
	}
	want := []TermKind{TermNonTerminal, TermTerminal, TermOptional, TermZeroOrMore, TermOneOrMore, TermGroup}
	for i, term := range terms {
		if term.Kind != want[i] {
			t.Fatalf("term %d kind = %d want %d", i, term.Kind, want[i])
		}
		if term.Sub == nil && want[i] != TermNonTerminal && want[i] != TermTerminal {
			t.Fatalf("term %d missing Sub", i)
		}
	}
	if len(Seq(terms...).Terms) != 6 {
		t.Fatal("Seq builder failed")
	}
	if len(Epsilon().Terms) != 0 {
		t.Fatal("Epsilon must be empty")
	}
}
