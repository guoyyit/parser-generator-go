// Package grammar provides the declarative model used to describe a grammar:
// lexical rules, productions, and their precedence/ignore constraints.
//
// A grammar is constructed with the fluent Builder (or literally with the
// value types). Example:
//
//	g, err := grammar.NewBuilder("calc", "1.0.0").
//	    Ignore("WS", `\s+`).
//	    Token("NUM", `[0-9]+`).
//	    Token("PLUS", `\+`, grammar.WithPriority(1)).
//	    Rule("expr",
//	        grammar.Seq(grammar.NT("term"), grammar.T("PLUS"), grammar.NT("expr")),
//	        grammar.Seq(grammar.NT("term")),
//	    ).
//	    Start("expr")
package grammar

// TermKind identifies the kind of an expression term within a production.
type TermKind int

const (
	// TermTerminal is a reference to a lexical unit.
	TermTerminal TermKind = iota
	// TermNonTerminal is a reference to another production.
	TermNonTerminal
	// TermOptional matches its child expression zero or one time.
	TermOptional
	// TermStar matches its child expression zero or more times.
	TermStar
	// TermPlus matches its child expression one or more times.
	TermPlus
	// TermGroup groups a child expression (used for grouping alternatives).
	TermGroup
)

// Term is one element of a production sequence.
type Term struct {
	Kind  TermKind
	Name  string    // terminal or non-terminal name
	Child *Expr     // non-nil for Optional/Star/Plus/Group
	Pos   Position
}

// Expr is an ordered sequence of terms (a concatenation); an empty sequence
// denotes epsilon.
type Expr struct {
	Terms []Term
	Pos   Position
}

// Rule is a production: Name -> alternatives.
type Rule struct {
	Name  string
	Alts  []Expr
	Pos   Position
	Start bool // true for the designated start rule
}

// TokenRule is a declarative lexical rule.
type TokenRule struct {
	Name     string
	Pattern  string // Go regexp syntax; matches a prefix of the input
	Ignore   bool   // tokens of this rule are hidden from the parser
	Priority int    // higher priority wins a longest-match tie
	Pos      Position
}

// Position points into the grammar definition source for diagnostics.
type Position struct {
	File string
	Line int
	Col  int
}

func (p Position) String() string {
	if p.File == "" && p.Line == 0 {
		return "<grammar>"
	}
	if p.File == "" {
		return "<grammar>:" + itoa(p.Line) + ":" + itoa(p.Col)
	}
	return p.File + ":" + itoa(p.Line) + ":" + itoa(p.Col)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Grammar is the complete declarative grammar definition.
type Grammar struct {
	Name    string
	Version string
	Tokens  []TokenRule
	Rules   []Rule
	Start   string
}

// Builder constructs grammar definitions using a small declarative DSL.
type Builder struct {
	g Grammar
}

// NewBuilder starts a grammar definition with the given name and version.
func NewBuilder(name, version string) *Builder {
	return &Builder{g: Grammar{Name: name, Version: version}}
}

// Token declares a lexical rule. Duplicate names are rejected at Start.
func (b *Builder) Token(name, pattern string, opts ...TokenOption) *Builder {
	return b.addToken(TokenRule{Name: name, Pattern: pattern}, opts)
}

// Ignore declares an ignored lexical rule (whitespace, comments, ...).
func (b *Builder) Ignore(name, pattern string, opts ...TokenOption) *Builder {
	return b.addToken(TokenRule{Name: name, Pattern: pattern, Ignore: true}, opts)
}

func (b *Builder) addToken(t TokenRule, opts []TokenOption) *Builder {
	c := tokenConfig{}
	for _, o := range opts {
		o(&c)
	}
	t.Priority = c.priority
	b.g.Tokens = append(b.g.Tokens, t)
	return b
}

// Rule declares a production with one or more alternatives. Declaring the
// same rule name more than once is rejected at Start.
func (b *Builder) Rule(name string, alts ...Expr) *Builder {
	b.g.Rules = append(b.g.Rules, Rule{Name: name, Alts: alts})
	return b
}

// Start marks the start rule, validates the whole definition and returns the
// finished grammar.
func (b *Builder) Start(name string) (*Grammar, error) {
	b.g.Start = name
	for i := range b.g.Rules {
		b.g.Rules[i].Start = b.g.Rules[i].Name == name
	}
	if err := b.g.Validate(); err != nil {
		return nil, err
	}
	g := b.g
	return &g, nil
}

// TokenOption customizes a lexical rule.
type TokenOption func(*tokenConfig)

type tokenConfig struct {
	priority int
}

// WithPriority sets a lexical rule's tie-breaking priority (higher wins on
// equal-length longest matches).
func WithPriority(p int) TokenOption {
	return func(c *tokenConfig) { c.priority = p }
}

// T builds a terminal reference term.
func T(name string) Term { return Term{Kind: TermTerminal, Name: name} }

// NT builds a non-terminal reference term.
func NT(name string) Term { return Term{Kind: TermNonTerminal, Name: name} }

// Opt wraps an expression as optional (zero or one occurrences).
func Opt(e Expr) Term {
	ec := e
	return Term{Kind: TermOptional, Child: &ec}
}

// Star wraps an expression as zero-or-more repetition.
func Star(e Expr) Term {
	ec := e
	return Term{Kind: TermStar, Child: &ec}
}

// Plus wraps an expression as one-or-more repetition.
func Plus(e Expr) Term {
	ec := e
	return Term{Kind: TermPlus, Child: &ec}
}

// Group wraps an expression for grouping.
func Group(e Expr) Term {
	ec := e
	return Term{Kind: TermGroup, Child: &ec}
}

// Seq builds an expression from a sequence of terms. It is also used to
// describe individual alternatives.
func Seq(terms ...Term) Expr {
	t := make([]Term, len(terms))
	copy(t, terms)
	return Expr{Terms: t}
}

// Alt is an alias for Seq describing one alternative.
func Alt(terms ...Term) Expr { return Seq(terms...) }
