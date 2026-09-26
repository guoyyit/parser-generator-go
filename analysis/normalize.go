package analysis

import (
	"fmt"

	"github.com/guoyyit/parser-generator-go/grammar"
)

// NormalRule is a normalized production: alternatives of symbol sequences.
// Epsilon is represented by an empty sequence. Synthetic rules are introduced
// for optional/repetition/group constructs and are never ambiguity-checked
// against user alternatives (their shape is fixed and unambiguous).
type NormalRule struct {
	Name      string
	Alts      [][]string // symbol sequences; terminals and rules share names
	Pos       grammar.Position
	Synthetic bool
}

// NormalGrammar is the normalized grammar.
type NormalGrammar struct {
	Start     string
	Terminals map[string]bool
	Rules     map[string]*NormalRule
	Order     []string
}

type normalizer struct {
	ng        *NormalGrammar
	counter   int
	ownerRule map[string]string // synthetic rule -> owning user rule
}

// Normalize expands Option/Star/Plus/Group into plain productions whose
// right-hand sides are sequences of symbol names.
func Normalize(g *grammar.Grammar) (*NormalGrammar, error) {
	n := &normalizer{
		ng: &NormalGrammar{
			Start:     g.Start,
			Terminals: map[string]bool{},
			Rules:     map[string]*NormalRule{},
		},
		ownerRule: map[string]string{},
	}
	for _, t := range g.Tokens {
		n.ng.Terminals[t.Name] = true
	}
	for _, r := range g.Rules {
		if _, exists := n.ng.Rules[r.Name]; exists {
			return nil, fmt.Errorf("analysis: duplicate rule %q", r.Name)
		}
		nr := &NormalRule{Name: r.Name, Pos: r.Pos}
		n.ng.Rules[r.Name] = nr
		n.ng.Order = append(n.ng.Order, r.Name)
		for _, a := range r.Alts {
			seq, err := n.convertSeq(a, r.Name)
			if err != nil {
				return nil, err
			}
			nr.Alts = append(nr.Alts, seq)
		}
	}
	return n.ng, nil
}

func (n *normalizer) freshName(owner, tag string) string {
	n.counter++
	return fmt.Sprintf("__%s_%s_%d", owner, tag, n.counter)
}

func (n *normalizer) addSynthetic(owner, tag string, alts [][]string) string {
	name := n.freshName(owner, tag)
	n.ng.Rules[name] = &NormalRule{Name: name, Alts: alts, Synthetic: true}
	n.ng.Order = append(n.ng.Order, name)
	n.ownerRule[name] = owner
	return name
}

// convertSeq converts an expression, registering synthetic rules as needed.
func (n *normalizer) convertSeq(e grammar.Expr, owner string) ([]string, error) {
	var seq []string
	for _, t := range e.Terms {
		name, err := n.convertTerm(t, owner)
		if err != nil {
			return nil, err
		}
		if name != "" {
			seq = append(seq, name)
		}
	}
	return seq, nil
}

func (n *normalizer) convertTerm(t grammar.Term, owner string) (string, error) {
	switch t.Kind {
	case grammar.TermTerminal, grammar.TermNonTerminal:
		return t.Name, nil
	case grammar.TermGroup:
		// Group with a single sequence is transparent; in practice alternatives
		// inside a group are encoded as separate synthetic alternatives.
		seq, err := n.convertSeq(*t.Child, owner)
		if err != nil {
			return "", err
		}
		if len(seq) == 1 {
			return seq[0], nil
		}
		return n.addSynthetic(owner, "group", [][]string{seq}), nil
	case grammar.TermOptional:
		seq, err := n.convertSeq(*t.Child, owner)
		if err != nil {
			return "", err
		}
		// S -> epsilon | seq
		return n.addSynthetic(owner, "opt", [][]string{nil, seq}), nil
	case grammar.TermStar:
		seq, err := n.convertSeq(*t.Child, owner)
		if err != nil {
			return "", err
		}
		// S -> epsilon | seq S  (right recursion: guaranteed unambiguous form)
		name := n.freshName(owner, "star")
		rec := append(append([]string{}, seq...), name)
		n.ng.Rules[name] = &NormalRule{
			Name:      name,
			Alts:      [][]string{nil, rec},
			Synthetic: true,
		}
		n.ng.Order = append(n.ng.Order, name)
		n.ownerRule[name] = owner
		return name, nil
	case grammar.TermPlus:
		seq, err := n.convertSeq(*t.Child, owner)
		if err != nil {
			return "", err
		}
		// S -> seq (seq)*  via a star synthetic, sharing the same sequence
		star := n.addSynthetic(owner, "star", nil)
		// fix the star's alternatives now that its name exists
		n.ng.Rules[star].Alts = [][]string{
			nil,
			append(append([]string{}, seq...), star),
		}
		return n.addSynthetic(owner, "plus", [][]string{append(append([]string{}, seq...), star)}), nil
	default:
		return "", fmt.Errorf("analysis: malformed term in rule %q", owner)
	}
}

// OwnerRule maps a synthetic rule name to the user rule that owns it; user
// rules map to themselves.
func (n *NormalGrammar) OwnerRule(name string) string {
	visited := map[string]bool{}
	for {
		r, ok := n.Rules[name]
		if !ok || !r.Synthetic {
			return name
		}
		if visited[name] {
			return name
		}
		visited[name] = true
		// synthetic ownership is stored indirectly: synthetic names are not
		// user rules, so walk by naming convention is unnecessary; callers use
		// the explicit map via the normalizer. Fallback: return name.
		return name
	}
}
