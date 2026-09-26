package lexer

import (
	"testing"

	"github.com/guoyyit/parser-generator-go/grammar"
)

func TestLongestMatch(t *testing.T) {
	lx, err := Compile([]grammar.LexerRule{
		{Name: "ID", Pattern: `[a-z]+`},
		{Name: "KW", Pattern: `if`},
	})
	if err != nil {
		t.Fatal(err)
	}
	toks, err := lx.Tokenize("iff")
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 1 || toks[0].Type != "ID" {
		t.Fatalf("longest match failed: %+v", toks)
	}
}

func TestPriorityOnEqualLength(t *testing.T) {
	lx, err := Compile([]grammar.LexerRule{
		{Name: "ID", Pattern: `if`, Priority: 1},
		{Name: "KW", Pattern: `if`, Priority: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	toks, err := lx.Tokenize("if")
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 1 || toks[0].Type != "KW" {
		t.Fatalf("priority failed: %+v", toks)
	}
}

func TestIdenticalPatternEqualPriorityRejected(t *testing.T) {
	_, err := Compile([]grammar.LexerRule{
		{Name: "A", Pattern: `abc`, Priority: 0},
		{Name: "B", Pattern: `abc`, Priority: 0},
	})
	if err == nil {
		t.Fatal("equal-priority identical patterns must be rejected")
	}
}

func TestDuplicateRuleNameRejected(t *testing.T) {
	_, err := Compile([]grammar.LexerRule{
		{Name: "A", Pattern: `a`},
		{Name: "A", Pattern: `b`},
	})
	if err == nil {
		t.Fatal("duplicate lexer rule names must be rejected")
	}
}

func TestWhitespaceIgnored(t *testing.T) {
	lx, err := Compile([]grammar.LexerRule{
		{Name: "A", Pattern: `a`},
		{Name: "B", Pattern: `b`},
		{Name: "WS", Pattern: `\s+`, Ignore: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	toks, err := lx.Tokenize("  a \n\t b  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 2 || toks[0].Type != "A" || toks[1].Type != "B" {
		t.Fatalf("ignore failed: %+v", toks)
	}
	if toks[1].Offset != 7 {
		t.Fatalf("offset tracking failed: got %d", toks[1].Offset)
	}
}

func TestNoMatchError(t *testing.T) {
	lx, err := Compile([]grammar.LexerRule{{Name: "A", Pattern: `a`}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = lx.Tokenize("ab")
	if err == nil {
		t.Fatal("expected lexer error")
	}
	le, ok := err.(*Error)
	if !ok || le.Offset != 1 {
		t.Fatalf("expected lexer error at offset 1, got %v", err)
	}
}
