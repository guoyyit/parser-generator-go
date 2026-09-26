package lexer

import (
	"strings"
	"testing"

	"github.com/guoyyit/parser-generator-go/grammar"
)

func TestLongestMatchAndIgnore(t *testing.T) {
	lx, err := Compile([]grammar.TokenRule{
		{Name: "WS", Pattern: `\s+`, Ignore: true},
		{Name: "NUM", Pattern: `[0-9]+`},
		{Name: "DOT", Pattern: `\.`},
		{Name: "FLOAT", Pattern: `[0-9]+\.[0-9]+`, Priority: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	toks, err := lx.Tokenize(" 3.14 ")
	if err != nil {
		t.Fatal(err)
	}
	vis := SkipIgnored(toks)
	if len(vis) != 1 || vis[0].Name != "FLOAT" || vis[0].Value != "3.14" {
		t.Fatalf("want one FLOAT, got %+v", vis)
	}
}

func TestPriorityTieBreak(t *testing.T) {
	// Equal pattern AND equal priority: rejected.
	if _, err := Compile([]grammar.TokenRule{
		{Name: "KW", Pattern: `if`},
		{Name: "ID", Pattern: `if`},
	}); err == nil || !strings.Contains(err.Error(), "identical pattern") {
		t.Fatalf("want identical-pattern conflict, got %v", err)
	}
	// Equal pattern, distinct priority: higher priority wins deterministically.
	lx, err := Compile([]grammar.TokenRule{
		{Name: "KW", Pattern: `if`, Priority: 5},
		{Name: "ID", Pattern: `if`},
	})
	if err != nil {
		t.Fatalf("distinct priorities should compile: %v", err)
	}
	toks, err := lx.Tokenize("if")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Name != "KW" {
		t.Fatalf("want KW on priority tie-break, got %s", toks[0].Name)
	}
}

func TestLineColumn(t *testing.T) {
	lx, err := Compile([]grammar.TokenRule{
		{Name: "WS", Pattern: `\s+`, Ignore: true},
		{Name: "ID", Pattern: `[a-z]+`},
	})
	if err != nil {
		t.Fatal(err)
	}
	toks, err := lx.Tokenize("ab\n cd")
	if err != nil {
		t.Fatal(err)
	}
	vis := SkipIgnored(toks)
	if len(vis) != 2 {
		t.Fatalf("want 2 tokens, got %d", len(vis))
	}
	if vis[1].Line != 2 || vis[1].Col != 2 {
		t.Fatalf("second token at 2:2, got %d:%d", vis[1].Line, vis[1].Col)
	}
}

func TestUnknownCharErrorLocation(t *testing.T) {
	lx, err := Compile([]grammar.TokenRule{
		{Name: "WS", Pattern: `\s+`, Ignore: true},
		{Name: "ID", Pattern: `[a-z]+`},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = lx.Tokenize("ab\n@")
	if err == nil {
		t.Fatal("want lex error")
	}
	le := err.(*Error)
	if le.Line != 2 || le.Col != 1 {
		t.Fatalf("want 2:1, got %d:%d", le.Line, le.Col)
	}
}

func TestRuntimeAmbiguityRejected(t *testing.T) {
	// Two rules match the same length with the same priority.
	lx, err := Compile([]grammar.TokenRule{
		{Name: "A", Pattern: `ab`},
		{Name: "B", Pattern: `ab`},
	})
	if err == nil || lx != nil {
		t.Fatal("compile-time conflict not rejected")
	}
}
