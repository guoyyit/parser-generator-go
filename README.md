# parser-generator-go

由语法定义驱动的本地解析器生成与运行时服务。调用方以声明式 API 描述
词法规则与产生式，服务完成校验、冲突检测并生成可并发复用的解析器；
解析输入时产出结构化语法树与精确到行列的错误定位。

## 特性

- **声明式语法描述**：词法规则支持优先级、最长匹配与空白忽略；
  产生式支持左递归、可选、重复（`*`/`+`）与分组。
- **可判定的冲突报告**：重复或冲突的声明被明确拒绝；
  存在歧义时拒绝生成，绝不静默选择（详见 [docs/grammar.md](docs/grammar.md)）。
- **左递归**：直接左递归与间接（互递归）左递归均可解析，
  运行时基于 Warth 等人的种子生长算法，左结合。
- **并发安全**：生成器无状态，产物不可变；
  同一解析器可被任意数量的 goroutine 并发调用，单次解析不触碰共享状态。
- **错误恢复**：快速失败 / 跳过 token / 同步词三种策略；
  恢复后的错误集合不漏报、不重复；无错误输入上恢复机制零额外代价。
- **增量校验**：局部修改只重算受影响的规则闭包，
  校验与再生成总耗时明显低于全量重建（见 `incremental` 包测试）。
- **版本兼容**：产物携带语法指纹；新旧语法不一致时给出逐条差异原因。

## 快速上手

```go
g := &grammar.Grammar{
    Name:  "calc",
    Start: "expr",
    LexerRules: []grammar.LexerRule{
        {Name: "NUM",  Pattern: `[0-9]+`},
        {Name: "PLUS", Pattern: `\+`},
        {Name: "WS",   Pattern: `\s+`, Ignore: true},
    ],
    Rules: []grammar.Rule{
        // expr = expr "+" num | num   —— 左递归、左结合
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
if err != nil { /* 无法校验 */ }
if !rep.OK()  { /* 冲突报告：rep.Diagnostics */ }

res := art.Parse("1 + 2 + 3", nil)          // 快速失败
res = art.Parse("1 + @ + 3", &parser.Config{ // 恢复模式
    Recovery: parser.RecoverySkipToken,
})
```

## 包结构

| 包 | 职责 |
|---|---|
| `grammar` | 声明式语法模型（词法规则、产生式、组合子） |
| `lexer` | 词法规则编译与 token 流（最长匹配、优先级、忽略） |
| `parser` | 解析运行时：左递归 packrat、错误定位与恢复、并发安全 |
| `generator` | 校验、冲突检测、解析器生成（无状态服务） |
| `incremental` | 增量校验与再生成（受影响闭包 + 工作清单） |
| `version` | 语法规范化表示、指纹与版本兼容性检查 |

## 测试

```sh
go test ./...          # 全部测试
go test ./... -race    # 含并发竞态检测
```

覆盖：左递归（直接/间接）、歧义冲突拒绝、错误恢复（不漏报不重复）、
并发复用、增量校验（正确性与加速比）、版本兼容性。
