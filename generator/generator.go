package generator

import (
	"fmt"

	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/lexer"
	"github.com/guoyyit/parser-generator-go/parser"
	"github.com/guoyyit/parser-generator-go/version"
)

// Generator 是无状态的解析器生成服务，可并发调用。
type Generator struct{}

// NewGenerator 构造生成器。生成器不持有任何状态，可在整个进程生命周期复用。
func NewGenerator() *Generator { return &Generator{} }

// Artifact 是生成成功的产物：一个可并发复用的解析器及其元信息。
// Artifact 构造后不可变。
type Artifact struct {
	parser   *parser.Parser
	grammar  *grammar.Grammar
	analysis *Analysis
	fp       version.Fingerprint
}

// Generate 校验语法并生成解析器。
//
// 返回的 report 始终非 nil；当 report.OK() 为 false 时 artifact 为 nil，
// 调用方应检查报告中的诊断（存在歧义时绝不静默生成）。
// err 仅用于语法对象本身为空等无法校验的情形。
func (gen *Generator) Generate(g *grammar.Grammar) (*Artifact, *ConflictReport, error) {
	if g == nil {
		return nil, nil, fmt.Errorf("generator: nil grammar")
	}
	analysis, rep := Analyze(g)
	if !rep.OK() {
		return nil, rep, nil
	}
	lx, err := lexer.Compile(g.LexerRules)
	if err != nil {
		rep.add(SeverityError, "", "lexer: %v", err)
		return nil, rep, nil
	}
	p, err := parser.New(g, lx, g.Start)
	if err != nil {
		rep.add(SeverityError, "", "parser: %v", err)
		return nil, rep, nil
	}
	gc := g.Clone()
	return &Artifact{
		parser:   p,
		grammar:  gc,
		analysis: analysis,
		fp:       version.FingerprintOf(gc),
	}, rep, nil
}

// Parse 用生成的解析器解析输入；cfg 可为 nil（快速失败）。
// 可并发调用，单次解析不修改共享状态。
func (a *Artifact) Parse(input string, cfg *parser.Config) *parser.Result {
	return a.parser.Parse(input, cfg)
}

// Parser 返回底层运行时解析器（只读，可并发复用）。
func (a *Artifact) Parser() *parser.Parser { return a.parser }

// Grammar 返回生成时语法定义的深拷贝。
func (a *Artifact) Grammar() *grammar.Grammar { return a.grammar.Clone() }

// Analysis 返回生成期的分析结果（只读，供增量校验与调试使用）。
func (a *Artifact) Analysis() *Analysis { return a.analysis }

// Fingerprint 返回生成时语法定义的指纹。
func (a *Artifact) Fingerprint() version.Fingerprint { return a.fp }

// CheckCompatibility 检查给定语法定义是否与本产物兼容；
// 不兼容时返回全部差异及明确原因。
func (a *Artifact) CheckCompatibility(g *grammar.Grammar) *version.Compatibility {
	return version.Check(a.grammar, g)
}

// ComputeDeps 计算规则间的直接依赖（结构性质，供增量校验使用）。
func ComputeDeps(g *grammar.Grammar) map[string]map[string]bool {
	deps := map[string]map[string]bool{}
	for i := range g.Rules {
		r := &g.Rules[i]
		if _, ok := deps[r.Name]; !ok {
			deps[r.Name] = map[string]bool{}
		}
		for _, alt := range r.Alternatives {
			walkTerms(alt.Terms, func(t *grammar.Term) {
				if t.Kind == grammar.TermNonTerminal {
					deps[r.Name][t.Name] = true
				}
			})
		}
	}
	return deps
}

// NewArtifact 用已构造的解析器组装产物（供增量校验复用缓存的词法器）。
// 不拷贝语法定义：调用方必须保证 g 在产物生命周期内不被修改。
func NewArtifact(p *parser.Parser, g *grammar.Grammar, a *Analysis) *Artifact {
	return NewArtifactWithFingerprint(p, g, a, version.FingerprintOf(g))
}

// NewArtifactWithFingerprint 与 NewArtifact 相同，但使用调用方提供的指纹
// （增量校验器按分段哈希局部更新指纹，避免全量重算）。
func NewArtifactWithFingerprint(p *parser.Parser, g *grammar.Grammar, a *Analysis, fp version.Fingerprint) *Artifact {
	return &Artifact{
		parser:   p,
		grammar:  g,
		analysis: a,
		fp:       fp,
	}
}
