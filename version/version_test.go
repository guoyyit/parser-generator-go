package version

import (
	"strings"
	"testing"

	"github.com/guoyyit/parser-generator-go/grammar"
)

func sampleGrammar() *grammar.Grammar {
	return &grammar.Grammar{
		Name:  "g",
		Start: "s",
		LexerRules: []grammar.LexerRule{
			{Name: "A", Pattern: `a`},
			{Name: "WS", Pattern: `\s+`, Ignore: true},
		},
		Rules: []grammar.Rule{
			{Name: "s", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.Terminal("A"), grammar.ZeroOrMore(grammar.Seq(grammar.Terminal("A")))),
			}},
		},
	}
}

func TestFingerprintStableAndOrderIndependent(t *testing.T) {
	g := sampleGrammar()
	fp1 := FingerprintOf(g)
	fp2 := FingerprintOf(sampleGrammar())
	if !fp1.Equal(fp2) {
		t.Fatal("fingerprint must be stable across equivalent grammars")
	}
	// 声明顺序变化不影响指纹。
	g2 := sampleGrammar()
	g2.LexerRules[0], g2.LexerRules[1] = g2.LexerRules[1], g2.LexerRules[0]
	if !FingerprintOf(g2).Equal(fp1) {
		t.Fatal("fingerprint must be independent of declaration order")
	}
}

func TestFingerprintDetectsChanges(t *testing.T) {
	base := FingerprintOf(sampleGrammar())

	g := sampleGrammar()
	g.Rules[0].Alternatives[0].Terms = append(g.Rules[0].Alternatives[0].Terms, grammar.Terminal("A"))
	if FingerprintOf(g).Equal(base) {
		t.Fatal("rule body change must change fingerprint")
	}

	g = sampleGrammar()
	g.LexerRules[0].Priority = 99
	if FingerprintOf(g).Equal(base) {
		t.Fatal("lexer priority change must change fingerprint")
	}

	g = sampleGrammar()
	g.Start = "other"
	if FingerprintOf(g).Equal(base) {
		t.Fatal("start rule change must change fingerprint")
	}
}

func TestCompatibilityNoMismatch(t *testing.T) {
	c := Check(sampleGrammar(), sampleGrammar())
	if !c.Compatible {
		t.Fatalf("identical grammars must be compatible: %v", c)
	}
}

func TestCompatibilityReportsReasons(t *testing.T) {
	old := sampleGrammar()

	newG := sampleGrammar()
	newG.Rules[0].Alternatives = append(newG.Rules[0].Alternatives, grammar.Epsilon())
	c := Check(old, newG)
	if c.Compatible {
		t.Fatal("changed rule must be incompatible")
	}
	if len(c.Changes) != 1 || c.Changes[0].Kind != ChangeRuleModified || c.Changes[0].Symbol != "s" {
		t.Fatalf("want rule modified s, got %v", c.Changes)
	}

	newG = sampleGrammar()
	newG.Rules = append(newG.Rules, grammar.Rule{Name: "extra", Alternatives: []grammar.Alternative{grammar.Epsilon()}})
	c = Check(old, newG)
	if c.Compatible {
		t.Fatal("added rule must be incompatible")
	}
	var foundAdd, foundStart bool
	for _, ch := range c.Changes {
		if ch.Kind == ChangeRuleAdded && ch.Symbol == "extra" {
			foundAdd = true
		}
		if ch.Kind == ChangeStartChanged {
			foundStart = true
		}
	}
	if !foundAdd {
		t.Fatalf("missing rule-added change: %v", c.Changes)
	}
	if foundStart {
		t.Fatal("start was unchanged")
	}

	newG = sampleGrammar()
	newG.Start = "s"
	newG.LexerRules[0].Pattern = `aa+`
	c = Check(old, newG)
	var lexMod bool
	for _, ch := range c.Changes {
		if ch.Kind == ChangeLexerModified && ch.Symbol == "A" {
			lexMod = true
			if !strings.Contains(ch.Detail, "a") {
				t.Fatalf("detail should show patterns: %s", ch.Detail)
			}
		}
	}
	if !lexMod {
		t.Fatalf("missing lexer-modified change: %v", c.Changes)
	}

	newG = sampleGrammar()
	newG.Rules = newG.Rules[:0]
	c = Check(old, newG)
	var removed bool
	for _, ch := range c.Changes {
		if ch.Kind == ChangeRuleRemoved && ch.Symbol == "s" {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("missing rule-removed change: %v", c.Changes)
	}
}

func TestCompatibilityErrorString(t *testing.T) {
	old := sampleGrammar()
	newG := sampleGrammar()
	newG.LexerRules = append(newG.LexerRules, grammar.LexerRule{Name: "B", Pattern: `b`})
	c := Check(old, newG)
	if c.Compatible || !strings.Contains(c.Error(), "lexer rule added") {
		t.Fatalf("error string must explain mismatch: %v", c)
	}
}
