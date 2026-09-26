package analysis

import (
	"fmt"
	"sort"

	"github.com/guoyyit/parser-generator-go/grammar"
)


type sets struct {
	nullable map[string]bool
	first    map[string]map[string]bool
	follow   map[string]map[string]bool
}

// ComputeNullable finds rules that can derive the empty string.
func (n *NormalGrammar) ComputeNullable() map[string]bool {
	nullable := map[string]bool{}
	changed := true
	for changed {
		changed = false
		for _, name := range n.Order {
			r := n.Rules[name]
			if nullable[name] {
				continue
			}
			for _, alt := range r.Alts {
				if n.seqNullable(alt, nullable) {
					nullable[name] = true
					changed = true
					break
				}
			}
		}
	}
	return nullable
}

func (n *NormalGrammar) seqNullable(seq []string, nullable map[string]bool) bool {
	for _, sym := range seq {
		if n.Terminals[sym] {
			return false
		}
		if !nullable[sym] {
			return false
		}
	}
	return true
}

// ComputeFirst computes the FIRST set of every rule using a least fixed
// point (left recursion is handled by iterative refinement).
func (n *NormalGrammar) ComputeFirst() map[string]map[string]bool {
	nullable := n.ComputeNullable()
	first := map[string]map[string]bool{}
	for _, name := range n.Order {
		first[name] = map[string]bool{}
	}
	changed := true
	for changed {
		changed = false
		for _, name := range n.Order {
			r := n.Rules[name]
			f := first[name]
			for _, alt := range r.Alts {
				for _, sym := range alt {
					if n.Terminals[sym] {
						if !f[sym] {
							f[sym] = true
							changed = true
						}
						break
					}
					for t := range first[sym] {
						if !f[t] {
							f[t] = true
							changed = true
						}
					}
					if !nullable[sym] {
						break
					}
				}
			}
		}
	}
	return first
}

// firstSeq returns FIRST of a sequence (without epsilon).
func (n *NormalGrammar) firstSeq(seq []string, nullable map[string]bool, first map[string]map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, sym := range seq {
		if n.Terminals[sym] {
			out[sym] = true
			return out
		}
		for t := range first[sym] {
			out[t] = true
		}
		if !nullable[sym] {
			return out
		}
	}
	return out
}

// ComputeFollow computes FOLLOW sets for all rules.
func (n *NormalGrammar) ComputeFollow() map[string]map[string]bool {
	nullable := n.ComputeNullable()
	first := n.ComputeFirst()
	follow := map[string]map[string]bool{}
	for _, name := range n.Order {
		follow[name] = map[string]bool{}
	}
	follow[n.Start][EOF] = true
	changed := true
	for changed {
		changed = false
		for _, rname := range n.Order {
			r := n.Rules[rname]
			for _, alt := range r.Alts {
				for i, sym := range alt {
					if n.Terminals[sym] {
						continue
					}
					rest := alt[i+1:]
					for t := range n.firstSeq(rest, nullable, first) {
						if !follow[sym][t] {
							follow[sym][t] = true
							changed = true
						}
					}
					if n.seqNullable(rest, nullable) {
						for t := range follow[rname] {
							if !follow[sym][t] {
								follow[sym][t] = true
								changed = true
							}
						}
					}
				}
			}
		}
	}
	return follow
}

func (n *NormalGrammar) sets() *sets {
	return &sets{
		nullable: n.ComputeNullable(),
		first:    n.ComputeFirst(),
		follow:   n.ComputeFollow(),
	}
}

func intersect(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Conflicts reports all decidable ambiguities for the supported grammar
// class. The check is:
//
//  1. Non-left-recursive alternatives of a rule must have pairwise disjoint
//     FIRST sets (an LL(1) predict-set check).
//  2. Among direct-left-recursive alternatives, the FIRST sets of the
//     continuations following the leading self-reference must be pairwise
//     disjoint; otherwise the growing-left-recursion parser could extend two
//     alternatives at once.
//  3. A rule may mix epsilon and left recursion only when its FOLLOW set is
//     disjoint from the branch FIRST sets.
//
// Synthetic rules produced by normalization have a fixed, unambiguous shape
// and are skipped.
func (n *NormalGrammar) Conflicts() []Conflict {
	s := n.sets()
	var out []Conflict
	for _, name := range n.Order {
		r := n.Rules[name]
		if r.Synthetic {
			continue
		}
		out = append(out, n.ruleConflicts(name, r, s)...)
	}
	return out
}

type branchKind int

const (
	bPlain branchKind = iota // does not begin with self
	bLeft                    // begins with self-reference
	bEpsilon                 // empty alternative
)

type branch struct {
	kind branchKind
	alt  []string
	pred map[string]bool // FIRST for plain; FIRST(continuation) for left
	idx  int
}

func (n *NormalGrammar) ruleConflicts(name string, r *NormalRule, s *sets) []Conflict {
	var out []Conflict
	var plain, left []branch
	hasEpsilon := false
	for idx, alt := range r.Alts {
		if len(alt) == 0 {
			hasEpsilon = true
			continue
		}
		if alt[0] == name {
			cont := alt[1:]
			left = append(left, branch{
				kind: bLeft,
				alt:  alt,
				pred: n.firstSeq(cont, s.nullable, s.first),
				idx:  idx,
			})
			continue
		}
		plain = append(plain, branch{
			kind: bPlain,
			alt:  alt,
			pred: n.firstSeq(alt, s.nullable, s.first),
			idx:  idx,
		})
	}

	// 1. plain-vs-plain FIRST overlap
	for i := 0; i < len(plain); i++ {
		for j := i + 1; j < len(plain); j++ {
			if both := intersect(plain[i].pred, plain[j].pred); len(both) > 0 {
				out = append(out, Conflict{
					Pos: r.Pos, Rule: name,
					Message: fmt.Sprintf(
						"ambiguous alternatives %d and %d in rule %q both start with %v; "+
							"the grammar does not decide which alternative to use (LL(1) FIRST conflict)",
						plain[i].idx+1, plain[j].idx+1, name, both),
				})
			}
		}
	}

	// 2. plain-vs-left: a left-recursive alternative is entered by first
	// growing a seed from a plain branch. If a plain branch and a left branch
	// share a leading token, both a seed and a recursive growth are possible.
	for i := 0; i < len(plain); i++ {
		for j := 0; j < len(left); j++ {
			// Seed growth continues only when the continuation after self
			// matches; overlap of plain FIRST with continuation FIRST is the
			// true ambiguity.
			if both := intersect(plain[i].pred, left[j].pred); len(both) > 0 {
				out = append(out, Conflict{
					Pos: r.Pos, Rule: name,
					Message: fmt.Sprintf(
						"alternative %d and left-recursive alternative %d in rule %q both continue with %v; "+
							"left-recursive growth is ambiguous (seed/continuation FIRST conflict)",
						plain[i].idx+1, left[j].idx+1, name, both),
				})
			}
		}
	}

	// 3. left-vs-left continuation overlap
	for i := 0; i < len(left); i++ {
		for j := i + 1; j < len(left); j++ {
			if both := intersect(left[i].pred, left[j].pred); len(both) > 0 {
				out = append(out, Conflict{
					Pos: r.Pos, Rule: name,
					Message: fmt.Sprintf(
						"left-recursive alternatives %d and %d in rule %q both continue with %v; "+
							"cannot decide how to grow the left-recursive parse",
						left[i].idx+1, left[j].idx+1, name, both),
				})
			}
		}
	}

	// 4. epsilon must not overlap left-recursive continuations or plain FIRST,
	// and must not clash with FOLLOW when left recursion exists.
	if hasEpsilon {
		epsilonStart := map[string]bool{}
		for _, b := range plain {
			for k := range b.pred {
				epsilonStart[k] = true
			}
		}
		follow := s.follow[name]
		if both := intersect(epsilonStart, follow); len(both) > 0 {
			out = append(out, Conflict{
				Pos: r.Pos, Rule: name,
				Message: fmt.Sprintf(
					"rule %q has an epsilon alternative while its body can start with %v also in FOLLOW(%q)=%v; "+
						"expanding or skipping epsilon cannot be decided",
					name, both, name, sortedKeys(follow)),
			})
		}
		if len(left) > 0 {
			var cont map[string]bool
			for _, b := range left {
				if cont == nil {
					cont = map[string]bool{}
				}
				for k := range b.pred {
					cont[k] = true
				}
			}
			if both := intersect(cont, follow); len(both) > 0 {
				out = append(out, Conflict{
					Pos: r.Pos, Rule: name,
					Message: fmt.Sprintf(
						"rule %q mixes epsilon and left recursion: continuation tokens %v also occur in FOLLOW(%q)=%v",
						name, both, name, sortedKeys(follow)),
				})
			}
		}
	}

	return out
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var _ = grammar.Position{}
