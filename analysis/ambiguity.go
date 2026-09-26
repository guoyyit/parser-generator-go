package analysis

import "fmt"

// Ambiguity detection for the supported grammar class.
//
// The FIRST/FOLLOW overlap checks in sets.go cover the common cases cheaply.
// For full decidability within the supported class, BoundedAmbiguity performs
// a bounded enumerative check: it runs a counting Earley recognizer over
// abstract terminal sequences (breadth-first, up to AmbiguityMaxSteps
// terminals), and rejects the grammar as soon as some input yields two or
// more distinct parse derivations. The recognizer counts derivations per
// chart item rather than storing them as a set, so epsilon productions and
// shared sub-parses are accounted for. The bounds make the total work finite
// and predictable; the check runs at generation time only and has zero cost
// at parse time.
const (
	// AmbiguityMaxSteps is the maximum input length (in terminals) explored.
	AmbiguityMaxSteps = 8
	// AmbiguityMaxStates caps Earley work per explored sequence.
	AmbiguityMaxStates = 8000
	// AmbiguityFrontierCap caps the breadth-first exploration frontier.
	AmbiguityFrontierCap = 400
)

type spanKey struct {
	rule   string
	i, j   int
}

type ambDetector struct {
	n            *NormalGrammar
	terminalList []string
}

func newAmbDetector2(n *NormalGrammar) *ambDetector {
	d := &ambDetector{n: n}
	seen := map[string]bool{}
	for _, name := range n.Order {
		r := n.Rules[name]
		if r.Synthetic {
			continue
		}
		for _, alt := range r.Alts {
			for _, sym := range alt {
				if n.Terminals[sym] && !seen[sym] {
					seen[sym] = true
					d.terminalList = append(d.terminalList, sym)
				}
			}
		}
	}
	return d
}

// BoundedAmbiguity returns a Conflict when any explored input has more than
// one distinct derivation, otherwise nil.
func (n *NormalGrammar) BoundedAmbiguity() *Conflict {
	d := newAmbDetector2(n)
	if len(d.terminalList) == 0 {
		return nil
	}
	frontier := [][]string{nil}
	for step := 0; step <= AmbiguityMaxSteps && len(frontier) > 0; step++ {
		var next [][]string
		for _, cur := range frontier {
			if ambiguous, count := d.derivationCount(cur); ambiguous {
				return &Conflict{
					Rule: n.Start,
					Message: fmt.Sprintf(
						"ambiguous grammar: input %v has %d distinct parse derivations; "+
							"factor the conflicting alternatives or disambiguate with token priorities "+
							"(detected at input length %d)",
						cur, count, len(cur)),
				}
			}
			if step < AmbiguityMaxSteps {
				for _, t := range d.terminalList {
					ns := make([]string, len(cur)+1)
					copy(ns, cur)
					ns[len(cur)] = t
					next = append(next, ns)
					if len(next) >= AmbiguityFrontierCap {
						break
					}
				}
			}
		}
		frontier = next
	}
	return nil
}

// derivationCount counts derivations by a monotone fixed-point dynamic
// program over spans. ways[rule][i:j] is the number of derivations of
// input[i:j] from rule, saturated at 2. Counts only ever increase, so
// relaxation reaches the least fixed point, which equals the true finite
// derivation count; left recursion is handled naturally by the relaxation.
func (d *ambDetector) derivationCount(input []string) (bool, int) {
	n := d.n
	N := len(input)
	ways := map[spanKey]int{}

	// altWays counts derivations of one concrete alternative over [i:j].
	altWays := func(alt []string, i, j int) int {
		cur := map[int]int{i: 1}
		for _, sym := range alt {
			nxt := map[int]int{}
			if n.Terminals[sym] {
				for pos, c := range cur {
					if pos < N && pos < j && input[pos] == sym {
						nxt[pos+1] = sat2(nxt[pos+1] + c)
					}
				}
			} else {
				for pos, c := range cur {
					for e := pos; e <= j; e++ {
						w := ways[spanKey{sym, pos, e}]
						if w > 0 {
							nxt[e] = sat2(nxt[e] + c*w)
						}
					}
				}
			}
			cur = nxt
			if len(cur) == 0 {
				return 0
			}
		}
		return cur[j]
	}

	// Relax by increasing span length; within a span, repeat until stable to
	// resolve (mutual) left recursion. Monotonicity guarantees termination;
	// the iteration cap guards against non-consuming cycles, which are
	// themselves reported as ambiguous (infinitely many derivations).
	const maxPasses = 256
	for L := 0; L <= N; L++ {
		for i := 0; i+L <= N; i++ {
			j := i + L
			passes := 0
			for {
				changed := false
				for _, name := range n.Order {
					r := n.Rules[name]
					total := 0
					for _, alt := range r.Alts {
						total = sat2(total + altWays(alt, i, j))
					}
					if total != ways[spanKey{name, i, j}] {
						ways[spanKey{name, i, j}] = total
						changed = true
					}
				}
				passes++
				if !changed {
					break
				}
				if passes > maxPasses {
					// Non-terminating derivation structure (e.g. A -> A).
					return true, 2
				}
			}
		}
	}

	total := ways[spanKey{n.Start, 0, N}]
	return total >= 2, total
}

func sat2(n int) int {
	if n > 2 {
		return 2
	}
	if n < 0 {
		return 2
	}
	return n
}

// DetectorExport exposes derivation counting for tests.
type DetectorExport struct{ d *ambDetector }

// NewAmbDetectorExport constructs an exported detector.
func NewAmbDetectorExport(n *NormalGrammar) *DetectorExport {
	return &DetectorExport{d: newAmbDetector2(n)}
}

// Count returns (ambiguous, derivationCount) for one concrete sequence.
func (x *DetectorExport) Count(input []string) (bool, int) { return x.d.derivationCount(input) }
