package generator_test

import (
	"fmt"

	"github.com/guoyyit/parser-generator-go/generator"
	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/parser"
)

// ExampleGenerate 展示声明式语法 -> 生成 -> 解析的完整流程，
// 包括左递归、可选/重复结构、空白忽略与错误定位。
func ExampleGenerator_Generate() {
	g := &grammar.Grammar{
		Name:  "calc",
		Start: "expr",
		LexerRules: []grammar.LexerRule{
			{Name: "NUM", Pattern: `[0-9]+`},
			{Name: "PLUS", Pattern: `\+`},
			{Name: "WS", Pattern: `\s+`, Ignore: true},
		},
		Rules: []grammar.Rule{
			// expr = expr "+" num | num —— 直接左递归，生成左结合树。
			{Name: "expr", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.NonTerminal("expr"), grammar.Terminal("PLUS"), grammar.NonTerminal("num")),
				grammar.Seq(grammar.NonTerminal("num")),
			}},
			{Name: "num", Alternatives: []grammar.Alternative{
				grammar.Seq(grammar.Terminal("NUM")),
			}},
		},
	}

	art, rep, err := generator.NewGenerator().Generate(g)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if !rep.OK() {
		fmt.Println(rep)
		return
	}

	// 正确输入。
	if r := art.Parse("1 + 2 + 3", nil); r.OK() {
		fmt.Println("parse ok, consumed all input")
	}

	// 错误输入：快速失败，行列定位。
	r := art.Parse("1 +\n+ 2", nil)
	for _, e := range r.Errors {
		fmt.Printf("%d:%d %s\n", e.Line, e.Column, e.Found)
	}

	// 恢复模式：跳过坏 token 继续解析，收集全部错误。
	rec := art.Parse("1 + @ + 3", &parser.Config{Recovery: parser.RecoverySkipToken})
	fmt.Println("recovered errors:", len(rec.Errors))
	// Output:
	// parse ok, consumed all input
	// 2:1 PLUS ("+")
	// recovered errors: 2
}
