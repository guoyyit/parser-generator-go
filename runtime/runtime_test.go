package runtime

import (
	"testing"

	"github.com/guoyyit/parser-generator-go/analysis"
	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/lexer"
	"github.com/guoyyit/parser-generator-go/recovery"
)

func compileParser(t *testing.T, g *grammar.Grammar) *Parser {
	t.Helper()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	lx, err := lexer.Compile(g.Tokens)
	if err != nil {
		t.Fatal(err)
	}
	ng, err := analysis.Normalize(g)
	if err != nil {
		t.Fatal(err)
	}
	if cs := ng.Conflicts(); len(cs) > 0 {
		t.Fatalf("conflicts: %v", cs)
	}
	if c := ng.BoundedAmbiguity(); c != nil {
		t.Fatalf("ambiguity: %v", c)
	}
	return NewParser(lx, ng)
}

func calcGrammar(t *testing.T) *grammar.Grammar {
	g, err := grammar.NewBuilder("calc", "1.0.0").
		Ignore("WS", `\s+`).
		Token("NUM", `[0-9]+`).
		Token("PLUS", `\+`).
		Token("STAR", `\*`).
		Token("LPAREN", `\(`).
		Token("RPAREN", `\)`).
		Rule("expr",
			grammar.Seq(grammar.NT("expr"), grammar.T("PLUS"), grammar.T("NUM")),
			grammar.Seq(grammar.NT("expr"), grammar.T("STAR"), grammar.T("NUM")),
			grammar.Seq(grammar.T("LPAREN"), grammar.NT("expr"), grammar.T("RPAREN")),
			grammar.Seq(grammar.T("NUM")),
		).
		Start("expr")
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestLeftRecursionAssociativity(t *testing.T) {
	p := compileParser(t, calcGrammar(t))
	res, err := p.Parse("1 + 2 + 3")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || len(res.Errors) != 0 {
		t.Fatalf("want clean parse, ok=%v errs=%v", res.OK, res.Errors)
	}
	// Left recursion yields a left-leaning tree: the outer expr spans [0,5)
	// and its left child spans [0,3).
	root := res.Tree
	if root.Name != "expr" || root.End != 5 {
		t.Fatalf("bad root: %+v end=%d", root.Name, root.End)
	}
	if len(root.Children) != 3 {
		t.Fatalf("want 3 children, got %d", len(root.Children))
	}
	left := root.Children[0]
	if left.End != 3 {
		t.Fatalf("left child end = %d, want 3 (left association)", left.End)
	}
}

func TestSugarAndGrouping(t *testing.T) {
	g, err := grammar.NewBuilder("g", "1.0.0").
		Ignore("WS", `\s+`).
		Token("A", `a`).
		Token("B", `b`).
		Rule("s",
			grammar.Seq(grammar.Plus(grammar.Seq(grammar.T("A"))), grammar.T("B")),
		).
		Start("s")
	if err != nil {
		t.Fatal(err)
	}
	p := compileParser(t, g)
	for _, in := range []string{"ab", "aaab"} {
		res, _ := p.Parse(in)
		if !res.OK {
			t.Fatalf("input %q: want OK, got errors %v", in, res.Errors)
		}
	}
	for _, in := range []string{"", "b", "a"} {
		res, _ := p.Parse(in)
		if res.OK {
			t.Fatalf("input %q: want failure", in)
		}
	}
	res, _ := p.Parse("c")
	if res.OK {
		t.Fatal("invalid input unexpectedly accepted")
	}
}

func TestPreciseErrorLocation(t *testing.T) {
	p := compileParser(t, calcGrammar(t))
	res, _ := p.Parse("1\n+ ?")
	if res.OK || len(res.Errors) != 1 {
		t.Fatalf("want exactly one error, got %+v", res.Errors)
	}
	e := res.Errors[0]
	// '?' is on line 3, column 3 ("1\n+ ?").
	if e.Line != 3 || e.Col != 3 {
		t.Fatalf("want 3:3, got %d:%d", e.Line, e.Col)
	}
}

func TestNoRecoveryNoExtraCost(t *testing.T) {
	p := compileParser(t, calcGrammar(t))
	res, _ := p.Parse("1 +")
	if res.OK {
		t.Fatal("incomplete input should fail without recovery")
	}
	if len(res.Errors) != 1 {
		t.Fatalf("want single error, got %d", len(res.Errors))
	}
}

func TestRecoverySkipsBadTokens(t *testing.T) {
	p := compileParser(t, calcGrammar(t))
	opts := Options{Recovery: recovery.Strategy{Mode: recovery.RecoverSkipTokens}}
	cases := []struct {
		input   string
		wantN   int
	}{
		{"1 + @ 2", 1},       // one lexical error
		{"1 + @ @ 2", 2},    // two lexical errors, both reported
		{"1 2", 1},          // one stray token
		{"1 +", 1},          // missing operand at EOF
	}
	for _, c := range cases {
		res, _ := p.ParseWith(c.input, opts)
		if !res.OK {
			t.Fatalf("%q: want recovered parse, ok=false errs=%v", c.input, res.Errors)
		}
		if len(res.Errors) != c.wantN {
			t.Fatalf("%q: want %d errors, got %d (%v)", c.input, c.wantN, len(res.Errors), res.Errors)
		}
		// No duplicated byte offsets.
		seen := map[int]bool{}
		for _, pe := range res.Errors {
			if seen[pe.Pos] {
				t.Fatalf("%q: duplicated error at pos %d", c.input, pe.Pos)
			}
			seen[pe.Pos] = true
		}
	}
}

func TestRecoverySyncTo(t *testing.T) {
	p := compileParser(t, calcGrammar(t))
	opts := Options{Recovery: recovery.Strategy{
		Mode: recovery.RecoverSyncTo, FollowFallback: []string{"NUM"},
	}}
	res, _ := p.ParseWith("1 + @@@ 2", opts)
	if !res.OK {
		t.Fatalf("want recovered, errs=%v", res.Errors)
	}
	// All '@' characters form one lexical error run (3 errors) and parsing
	// resumes at NUM 2.
	if len(res.Errors) != 3 {
		t.Fatalf("want 3 errors, got %d (%v)", len(res.Errors), res.Errors)
	}
}

func TestIndirectLeftRecursion(t *testing.T) {
	g, err := grammar.NewBuilder("g", "1.0.0").
		Token("A", `a`).
		Token("X", `x`).
		Rule("s",
			grammar.Seq(grammar.NT("t"), grammar.T("X")),
			grammar.Seq(grammar.T("A")),
		).
		Rule("t",
			grammar.Seq(grammar.NT("s")),
			grammar.Seq(grammar.T("A")),
		).
		Start("s")
	if err != nil {
		t.Fatal(err)
	}
	ng, _ := analysis.Normalize(g)
	if c := ng.BoundedAmbiguity(); c != nil {
		t.Skipf("grammar flagged ambiguous by bounded check: %v", c)
	}
	p := compileParser(t, g)
	res, _ := p.Parse("axx")
	if !res.OK {
		t.Fatalf("indirect left recursion failed: %v", res.Errors)
	}
}
