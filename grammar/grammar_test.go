package grammar

import (
	"strings"
	"testing"
)

func TestBuilderValid(t *testing.T) {
	g, err := NewBuilder("g", "1.0.0").
		Ignore("WS", `\s+`).
		Token("NUM", `[0-9]+`).
		Token("PLUS", `\+`, WithPriority(1)).
		Rule("expr",
			Seq(T("NUM")),
			Seq(NT("expr"), T("PLUS"), T("NUM")),
			Seq(Opt(Seq(T("NUM")))),
			Seq(Star(Seq(T("NUM")))),
			Seq(Plus(Seq(T("NUM")))),
			Seq(Group(Seq(T("NUM")))),
		).
		Start("expr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.Start != "expr" {
		t.Fatalf("start = %q, want expr", g.Start)
	}
	if len(g.Tokens) != 3 {
		t.Fatalf("tokens = %d, want 3", len(g.Tokens))
	}
}

func TestRejectsDuplicateToken(t *testing.T) {
	_, err := NewBuilder("g", "1.0.0").
		Token("NUM", `[0-9]+`).
		Token("NUM", `[0-9]+`).
		Rule("s", Seq(T("NUM"))).
		Start("s")
	if err == nil || !strings.Contains(err.Error(), "duplicate token rule") {
		t.Fatalf("want duplicate token error, got %v", err)
	}
}

func TestRejectsDuplicateRule(t *testing.T) {
	_, err := NewBuilder("g", "1.0.0").
		Token("NUM", `[0-9]+`).
		Rule("s", Seq(T("NUM"))).
		Rule("s", Seq(T("NUM"))).
		Start("s")
	if err == nil || !strings.Contains(err.Error(), "duplicate production rule") {
		t.Fatalf("want duplicate production error, got %v", err)
	}
}

func TestRejectsDanglingReference(t *testing.T) {
	_, err := NewBuilder("g", "1.0.0").
		Token("NUM", `[0-9]+`).
		Rule("s", Seq(NT("missing"))).
		Start("s")
	if err == nil || !strings.Contains(err.Error(), "unknown non-terminal") {
		t.Fatalf("want dangling reference error, got %v", err)
	}
}

func TestRejectsEmptyMatchPattern(t *testing.T) {
	_, err := NewBuilder("g", "1.0.0").
		Token("X", `a*`).
		Rule("s", Seq(Opt(Seq(T("X"))))).
		Start("s")
	if err == nil || !strings.Contains(err.Error(), "empty string") {
		t.Fatalf("want empty-match error, got %v", err)
	}
}

func TestRejectsUndeclaredStart(t *testing.T) {
	_, err := NewBuilder("g", "1.0.0").
		Token("NUM", `[0-9]+`).
		Rule("s", Seq(T("NUM"))).
		Start("nope")
	if err == nil || !strings.Contains(err.Error(), "start rule") {
		t.Fatalf("want undeclared start error, got %v", err)
	}
}

func TestParseVersion(t *testing.T) {
	if _, _, err := ParseVersion("1.2.3"); err != nil {
		t.Fatalf("valid version rejected: %v", err)
	}
	if _, _, err := ParseVersion("v1"); err == nil {
		t.Fatal("invalid version accepted")
	}
}
