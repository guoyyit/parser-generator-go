package parser

import (
	"sort"
	"sync"
	"testing"

	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/lexer"
)

func listParser(t *testing.T) *Parser {
	t.Helper()
	g := &grammar.Grammar{
		Name: "list",
		LexerRules: []grammar.LexerRule{
			{Name: "NUM", Pattern: `[0-9]+`},
			{Name: "COMMA", Pattern: `,`},
			{Name: "EXCL", Pattern: `!`},
			{Name: "WS", Pattern: `\s+`, Ignore: true},
		},
		Rules: []grammar.Rule{
			{Name: "list", Alternatives: []grammar.Alternative{
				grammar.Seq(
					grammar.Terminal("NUM"),
					grammar.ZeroOrMore(grammar.Seq(grammar.Terminal("COMMA"), grammar.Terminal("NUM"))),
				),
			}},
		},
		Start: "list",
	}
	lx, err := lexer.Compile(g.LexerRules)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(g, lx, "list")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func distinctOffsets(errs []*Error) ([]int, bool) {
	seen := map[int]bool{}
	var out []int
	for _, e := range errs {
		if seen[e.Offset] {
			return nil, false
		}
		seen[e.Offset] = true
		out = append(out, e.Offset)
	}
	sort.Ints(out)
	return out, true
}

func TestFailFastSingleError(t *testing.T) {
	p := listParser(t)
	res := p.Parse("1, 2, !, 4", nil)
	if res.OK() {
		t.Fatal("expected failure")
	}
	if len(res.Errors) != 1 {
		t.Fatalf("want exactly 1 error, got %d: %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Line != 1 || e.Column != 7 {
		t.Fatalf("want 1:7, got %d:%d (%s)", e.Line, e.Column, e)
	}
	if res.Root != nil {
		t.Fatal("fail-fast must not return a tree")
	}
}

func TestSkipTokenRecoveryNoMissNoDuplicate(t *testing.T) {
	p := listParser(t)
	cfg := &Config{Recovery: RecoverySkipToken}
	res := p.Parse("1, 2, !, 4", cfg)
	if res.OK() {
		t.Fatal("expected recovered errors")
	}
	if len(res.Errors) != 2 {
		t.Fatalf("want 2 errors, got %d: %v", len(res.Errors), res.Errors)
	}
	offsets, ok := distinctOffsets(res.Errors)
	if !ok {
		t.Fatal("error offsets must be distinct (no duplicates)")
	}
	if len(offsets) != 2 {
		t.Fatalf("bad offsets: %v", offsets)
	}
	if res.Root == nil || len(res.Root.Children) != 2 {
		t.Fatalf("expected 2 recovered fragments, got %+v", res.Root)
	}
	// 坏区域必须被覆盖：错误必须发生在坏 token 区域内。
	if offsets[0] != 6 {
		t.Fatalf("first error offset want 6, got %d", offsets[0])
	}
}

func TestSyncRecoverySkipsRegion(t *testing.T) {
	p := listParser(t)
	// 坏区域 "! 9 8"：跳过恢复每 token 报，同步恢复跳到逗号，错误更少。
	input := "1, ! ! ! , 4"
	skipCfg := &Config{Recovery: RecoverySkipToken}
	syncCfg := &Config{Recovery: RecoverySync, SyncTokens: map[string]bool{"COMMA": true}}

	skipRes := p.Parse(input, skipCfg)
	syncRes := p.Parse(input, syncCfg)
	if len(skipRes.Errors) <= len(syncRes.Errors) {
		t.Fatalf("sync recovery should report fewer errors: skip=%d sync=%d",
			len(skipRes.Errors), len(syncRes.Errors))
	}
	if len(syncRes.Errors) == 0 {
		t.Fatal("sync recovery must still report errors")
	}
	for i := 1; i < len(syncRes.Errors); i++ {
		if syncRes.Errors[i].Offset <= syncRes.Errors[i-1].Offset {
			t.Fatal("error offsets must strictly increase")
		}
	}
}

func TestMaxErrors(t *testing.T) {
	p := listParser(t)
	cfg := &Config{Recovery: RecoverySkipToken, MaxErrors: 1}
	res := p.Parse("1, !, !, 4", cfg)
	if len(res.Errors) != 1 {
		t.Fatalf("MaxErrors=1 must yield 1 error, got %d", len(res.Errors))
	}
}

func TestRuneColumn(t *testing.T) {
	p := listParser(t)
	// "1, é, 3"：é 处词法失败；é 占 2 字节但只有 1 列宽。
	res := p.Parse("1, é, 3", nil)
	if len(res.Errors) != 1 {
		t.Fatalf("want 1 error, got %d: %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Line != 1 || e.Column != 4 {
		t.Fatalf("rune column want 4, got %d:%d (offset=%d)", e.Line, e.Column, e.Offset)
	}
	if e.Offset != 3 {
		t.Fatalf("byte offset want 3, got %d", e.Offset)
	}
}

func TestMultiLineLocation(t *testing.T) {
	p := listParser(t)
	res := p.Parse("1, 2,\n3, !,\n4", nil)
	if len(res.Errors) != 1 {
		t.Fatalf("want 1 error, got %d", len(res.Errors))
	}
	e := res.Errors[0]
	if e.Line != 2 || e.Column != 4 {
		t.Fatalf("want 2:4, got %d:%d", e.Line, e.Column)
	}
}

func TestConcurrentParseReuse(t *testing.T) {
	p := buildExprParser(t)
	const goroutines = 64
	const per = 100
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*per)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				in := "1"
				for j := 0; j < i%7; j++ {
					in += "+2"
				}
				res := p.Parse(in, nil)
				if !res.OK() {
					errs <- res.Errors[0]
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent parse failed: %v", e)
	}
}

func TestRecoveredTreeDoesNotMutateSharedState(t *testing.T) {
	p := listParser(t)
	cfg := &Config{Recovery: RecoverySkipToken}
	r1 := p.Parse("1, !, 2", cfg)
	r2 := p.Parse("1, 2, 3", nil)
	if !r2.OK() {
		t.Fatalf("clean parse after error parse failed: %v", r2.Errors)
	}
	if len(r1.Errors) != 2 {
		t.Fatalf("first parse error count changed: %d", len(r1.Errors))
	}
}
