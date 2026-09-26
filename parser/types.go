// Package parser 提供由 generator 产出的解析器运行时。
//
// 解析器为只读共享结构，可并发执行 Parse；单次解析的全部可变状态
// （备忘录、错误收集器、行列缓存）都挂在每次调用独立的 session 上。
package parser

import (
	"fmt"
	"sort"
	"strings"
)

// Node 是解析结果的树节点。
type Node struct {
	// Symbol 为产生式名（非终结符）或词法单元名（终结符）。
	Symbol string
	// Value 仅终结符节点有效，为匹配的原始文本。
	Value string
	// Children 为非终结符节点的子节点。
	Children []*Node
	// Offset 为节点在输入中的起始字节偏移。
	Offset int
	// End 为节点覆盖范围的结束字节偏移（不含）。
	End int
}

// IsTerminal 报告节点是否为终结符（叶子且携带词法值）。
func (n *Node) IsTerminal() bool { return n != nil && len(n.Children) == 0 }

// RecoveryMode 决定解析遇到错误后的恢复策略。
type RecoveryMode int

const (
	// RecoveryFailFast 遇到第一个错误立即停止（默认，零恢复开销）。
	RecoveryFailFast RecoveryMode = iota
	// RecoverySkipToken 丢弃一个 token 后从当前规则继续尝试。
	RecoverySkipToken
	// RecoverySync 跳过 token 直到遇到同步词集合中的符号（或输入末尾）。
	RecoverySync
)

// Config 控制单次解析的行为。
type Config struct {
	// Recovery 为恢复策略，默认 RecoveryFailFast。
	Recovery RecoveryMode
	// SyncTokens 为 RecoverySync 模式下的同步词集合（词法单元名）。
	SyncTokens map[string]bool
	// MaxErrors 限制收集的错误数量上限，0 表示不限制。
	MaxErrors int
}

// Error 是带精确行列定位的解析错误。
type Error struct {
	Offset   int      // 字节偏移
	Line     int      // 1 起始行号
	Column   int      // 1 起始列号（按 rune 计）
	Expected []string // 期望的符号（排序去重后）
	Found    string   // 实际遇到的符号描述
	Msg      string   // 人类可读描述
	// FromLexer 报告错误是否来自词法阶段（否则来自语法阶段）。
	FromLexer bool
}

func (e *Error) Error() string {
	if len(e.Expected) > 0 {
		return fmt.Sprintf("line %d, column %d: expected %s, found %s",
			e.Line, e.Column, strings.Join(e.Expected, " or "), e.Found)
	}
	return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

// Result 是一次解析的输出。
type Result struct {
	// Root 为解析树；完全失败时为 nil。
	Root *Node
	// Errors 为解析过程中收集到的错误（恢复模式下可能非空且 Root 仍可用）。
	Errors []*Error
	// Consumed 为成功消费的字节数。
	Consumed int
}

// OK 报告解析是否完全成功（无错误且消费了全部输入）。
func (r *Result) OK() bool { return len(r.Errors) == 0 && r.Root != nil }

// lineIndex 惰性计算的行起始偏移表，仅在出错路径上构建，
// 保证无错误输入不付出额外代价。
type lineIndex struct {
	input  string
	starts []int // 每行第一个字节的偏移，starts[0] == 0
	done   bool
}

func newLineIndex(input string) *lineIndex {
	return &lineIndex{input: input, starts: []int{0}}
}

func (li *lineIndex) build() {
	if li.done {
		return
	}
	li.done = true
	for i := 0; i < len(li.input); i++ {
		if li.input[i] == '\n' && i+1 < len(li.input) {
			li.starts = append(li.starts, i+1)
		}
	}
}

// locate 把字节偏移换算为 1 起始的行列（列按 rune 计）。
func (li *lineIndex) locate(offset int) (line, col int) {
	li.build()
	if offset < 0 {
		offset = 0
	}
	if offset > len(li.input) {
		offset = len(li.input)
	}
	// starts 递增，二分查找最后一个 <= offset 的行起点。
	i := sort.Search(len(li.starts), func(i int) bool { return li.starts[i] > offset }) - 1
	if i < 0 {
		i = 0
	}
	line = i + 1
	col = 1
	for j := li.starts[i]; j < offset; j += lenRune(li.input, j) {
		col++
	}
	return line, col
}

func lenRune(s string, i int) int {
	c := s[i]
	switch {
	case c < 0x80:
		return 1
	case c < 0xE0:
		return 2
	case c < 0xF0:
		return 3
	default:
		return 4
	}
}
