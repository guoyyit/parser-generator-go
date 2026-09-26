package generator

import (
	"strings"
	"sync"
	"testing"

	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/parser"
	"github.com/guoyyit/parser-generator-go/version"
)

// TestConcurrentGenerate 验证无状态生成器可被并发安全复用。
func TestConcurrentGenerate(t *testing.T) {
	gen := NewGenerator()
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g := exprGrammar()
			g.Name = "expr"
			art, rep, err := gen.Generate(g)
			if err != nil {
				errs <- err
				return
			}
			if !rep.OK() || art == nil {
				errs <- rep
				return
			}
			res := art.Parse("1+2*3", nil)
			if !res.OK() {
				errs <- res.Errors[0]
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent generate failed: %v", e)
	}
}

// TestArtifactFingerprintCompatibility 验证产物与新版本语法的不匹配被识别。
func TestArtifactFingerprintCompatibility(t *testing.T) {
	gen := NewGenerator()
	g := exprGrammar()
	art, _, err := gen.Generate(g)
	if err != nil {
		t.Fatal(err)
	}
	// 兼容：同一份语法。
	if c := art.CheckCompatibility(g); !c.Compatible {
		t.Fatalf("identical grammar must be compatible: %v", c)
	}
	// 不兼容：修改了产生式。
	g2 := g.Clone()
	g2.Rules[2].Alternatives[0].Terms = append(g2.Rules[2].Alternatives[0].Terms, grammar.Terminal("PLUS"))
	c := art.CheckCompatibility(g2)
	if c.Compatible {
		t.Fatal("artifact must detect grammar drift")
	}
	if !strings.Contains(c.Error(), "rule modified") {
		t.Fatalf("compatibility error must explain reason: %v", c)
	}
	// 指纹随之变化。
	if art.Fingerprint() == version.FingerprintOf(g2) {
		t.Fatal("fingerprints must differ after modification")
	}
}

// TestConcurrentArtifactParse 验证同一生成器产出的解析器支持并发解析（-race）。
func TestConcurrentArtifactParse(t *testing.T) {
	art, _, err := NewGenerator().Generate(exprGrammar())
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan *parser.Error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			cfg := &parser.Config{Recovery: parser.RecoverySkipToken, MaxErrors: 3}
			for j := 0; j < 50; j++ {
				res := art.Parse("1+2+@+4+5", cfg)
				_ = res
				res2 := art.Parse("1+2+3", nil)
				if !res2.OK() {
					errs <- res2.Errors[0]
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent parse error: %v", e)
	}
}

// TestArtifactGrammarImmutability 验证调用方对语法的后续修改不影响产物。
func TestArtifactGrammarImmutability(t *testing.T) {
	g := exprGrammar()
	art, _, err := NewGenerator().Generate(g)
	if err != nil {
		t.Fatal(err)
	}
	before := art.Fingerprint()
	// 调用方修改原始语法对象。
	g.Rules[0].Alternatives[0].Terms[2] = grammar.NonTerminal("atom")
	g.LexerRules[0].Pattern = `[0-9][0-9]`
	if art.Fingerprint() != before {
		t.Fatal("artifact fingerprint must not change when caller mutates grammar")
	}
	if r := art.Parse("1+2*3+4", nil); !r.OK() {
		t.Fatalf("artifact must keep working after caller mutation: %v", r.Errors)
	}
}
