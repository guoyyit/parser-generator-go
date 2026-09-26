// Package lexer 把声明式词法规则编译为可并发复用的词法分析器。
//
// 匹配语义：最长匹配优先；长度相同取 Priority 数值大者；
// 长度与优先级都相同视为冲突，在编译期被拒绝。
// Ignore 为 true 的规则产生的 token 会被丢弃。
package lexer

import (
	"fmt"
	"regexp"

	"github.com/guoyyit/parser-generator-go/grammar"
)

// Token 是词法分析的输出单元。
type Token struct {
	Type   string // 对应 LexerRule.Name
	Value  string // 匹配的原始文本
	Offset int    // 在输入中的字节偏移
}

// Error 描述一次词法失败，携带字节偏移，行列换算由 parser 层完成。
type Error struct {
	Offset int
	Msg    string
}

func (e *Error) Error() string { return fmt.Sprintf("lexer error at offset %d: %s", e.Offset, e.Msg) }

// Lexer 是编译后的词法分析器，只读，可并发复用。
type Lexer struct {
	rules []compiledRule
}

type compiledRule struct {
	name    string
	re      *regexp.Regexp
	prio    int
	ignore  bool
	pattern string
}

// Compile 校验并编译词法规则。
// 重复的规则名、非法正则、相同优先级的等价模式冲突都会被拒绝。
func Compile(rules []grammar.LexerRule) (*Lexer, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("lexer: no lexer rules declared")
	}
	seen := make(map[string]bool, len(rules))
	lx := &Lexer{rules: make([]compiledRule, 0, len(rules))}
	for _, r := range rules {
		if r.Name == "" {
			return nil, fmt.Errorf("lexer: rule with empty name")
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("lexer: duplicate rule name %q", r.Name)
		}
		seen[r.Name] = true
		if r.Pattern == "" {
			return nil, fmt.Errorf("lexer: rule %q has empty pattern", r.Name)
		}
		re, err := regexp.Compile("^(?:" + r.Pattern + ")")
		if err != nil {
			return nil, fmt.Errorf("lexer: rule %q invalid pattern: %w", r.Name, err)
		}
		lx.rules = append(lx.rules, compiledRule{
			name:    r.Name,
			re:      re,
			prio:    r.Priority,
			ignore:  r.Ignore,
			pattern: r.Pattern,
		})
	}
	if err := detectPatternConflicts(lx.rules); err != nil {
		return nil, err
	}
	return lx, nil
}

// detectPatternConflicts 检查同优先级的规则是否可能匹配相同文本。
// 采用保守策略：仅当两条同优先级规则的正则文本完全相同时判定为确定冲突；
// 其余潜在重叠在运行期以"等长等优先级"条件精确捕获。
func detectPatternConflicts(rules []compiledRule) error {
	type key struct {
		prio    int
		pattern string
	}
	seen := map[key]string{}
	for _, r := range rules {
		k := key{prio: r.prio, pattern: r.pattern}
		if prev, ok := seen[k]; ok {
			return fmt.Errorf("lexer: rules %q and %q have identical pattern and priority; match would be ambiguous", prev, r.name)
		}
		seen[k] = r.name
	}
	return nil
}

// Next 从 input 的 offset 处读取下一个非忽略 token。
// 返回 (token, 新offset, error)；到达输入末尾时返回 ok=false。
func (l *Lexer) Next(input string, offset int) (tok Token, next int, ok bool, err error) {
	pos := offset
	for {
		if pos >= len(input) {
			return Token{}, pos, false, nil
		}
		t, n, e := l.matchOne(input, pos)
		if e != nil {
			return Token{}, pos, false, e
		}
		if n == pos {
			// 空匹配保护：避免死循环（例如规则可匹配空串）。
			return Token{}, pos, false, &Error{Offset: pos, Msg: fmt.Sprintf("rule %q matched empty string", t.Type)}
		}
		if !l.isIgnored(t.Type) {
			return t, n, true, nil
		}
		pos = n
	}
}

// Tokenize 把整段输入切成 token 流（忽略规则不产出）。
// 出错时返回已切出的前缀与错误。
func (l *Lexer) Tokenize(input string) ([]Token, error) {
	var out []Token
	pos := 0
	for {
		tok, next, ok, err := l.Next(input, pos)
		if err != nil {
			return out, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, tok)
		pos = next
	}
}

func (l *Lexer) matchOne(input string, pos int) (Token, int, error) {
	rest := input[pos:]
	var (
		best    *compiledRule
		bestLen = -1
		tied    string
	)
	for i := range l.rules {
		r := &l.rules[i]
		loc := r.re.FindStringIndex(rest)
		if loc == nil || loc[0] != 0 {
			continue
		}
		l := loc[1]
		switch {
		case l > bestLen:
			bestLen = l
			best = r
			tied = ""
		case l == bestLen && best != nil && r.name != best.name:
			// 等长：优先级高者胜；同优先级记为平局冲突。
			if r.prio > best.prio {
				best = r
				tied = ""
			} else if r.prio == best.prio {
				tied = r.name
			}
		}
	}
	if best == nil {
		return Token{}, pos, &Error{Offset: pos, Msg: fmt.Sprintf("no lexer rule matches input starting with %q", preview(rest))}
	}
	if tied != "" {
		return Token{}, pos, &Error{Offset: pos, Msg: fmt.Sprintf("ambiguous token: rules %q and %q match %d bytes with equal priority", best.name, tied, bestLen)}
	}
	return Token{Type: best.name, Value: rest[:bestLen], Offset: pos}, pos + bestLen, nil
}

func (l *Lexer) isIgnored(name string) bool {
	for i := range l.rules {
		if l.rules[i].name == name {
			return l.rules[i].ignore
		}
	}
	return false
}

func preview(s string) string {
	const n = 16
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// TokenTypes 返回所有会产出 token 的规则名（忽略规则除外），按声明顺序。
func (l *Lexer) TokenTypes() []string {
	var out []string
	for _, r := range l.rules {
		if !r.ignore {
			out = append(out, r.name)
		}
	}
	return out
}
