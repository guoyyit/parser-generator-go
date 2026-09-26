// Package incremental provides cached, incremental grammar validation.
//
// Each declaration unit (token rule or production) has a content fingerprint.
// On a re-check after a local edit, only units whose fingerprint changed are
// revalidated, together with the reverse-dependency closure of the changed
// productions (rules that reference them, transitively); unaffected units are
// served from the cache. Both the set of recomputed units and the performed
// work count are exposed so callers (and tests) can verify that incremental
// work is substantially below a full rebuild.
package incremental

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/guoyyit/parser-generator-go/grammar"
)

// unit is one cacheable declaration unit.
type unit struct {
	kind string // "token" or "rule"
	name string
	fp   string
	err  error // validation error associated with this unit, if any
}

// Engine caches per-unit validation and invalidates only affected units.
// It is safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	units    map[string]*unit
	edges    map[string]map[string]bool // rule -> rules it references
	revEdges map[string]map[string]bool // referenced rule -> referencing rules
	hasPrev  bool
}

// NewEngine creates an incremental validation engine.
func NewEngine() *Engine {
	return &Engine{
		units:    map[string]*unit{},
		edges:    map[string]map[string]bool{},
		revEdges: map[string]map[string]bool{},
	}
}

// Report is the outcome of an incremental validation.
type Report struct {
	Valid       bool
	Errors      []error
	Recomputed  []string // units actually recomputed in this pass
	RecomputedN int      // number of recomputed units
	TotalUnits  int      // total declaration units in the grammar
}

// fingerprint computes a stable content fingerprint for one rule.
func ruleFingerprint(r grammar.Rule) string {
	h := sha256.New()
	var walk func(e grammar.Expr)
	walk = func(e grammar.Expr) {
		for _, t := range e.Terms {
			fmt.Fprintf(h, "%d:%s:", t.Kind, t.Name)
			if t.Child != nil {
				walk(*t.Child)
			}
		}
	}
	for _, a := range r.Alts {
		walk(a)
		h.Write([]byte{'|'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func tokenFingerprint(t grammar.TokenRule) string {
	return fmt.Sprintf("%d|%v|%s", t.Priority, t.Ignore, t.Pattern)
}

// references returns the non-terminal names referenced by a rule.
func references(r grammar.Rule) []string {
	set := map[string]bool{}
	var walk func(e grammar.Expr)
	walk = func(e grammar.Expr) {
		for _, t := range e.Terms {
			if t.Kind == grammar.TermNonTerminal {
				set[t.Name] = true
			}
			if t.Child != nil {
				walk(*t.Child)
			}
		}
	}
	for _, a := range r.Alts {
		walk(a)
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Check validates a (possibly edited) grammar, reusing cached results for
// unaffected units.
func (e *Engine) Check(g *grammar.Grammar) (*Report, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Build the current unit set and dependency graph.
	cur := map[string]*unit{}
	edges := map[string]map[string]bool{}
	addUnit := func(key string, u *unit) { cur[key] = u }
	for _, t := range g.Tokens {
		addUnit("token:"+t.Name, &unit{kind: "token", name: t.Name, fp: tokenFingerprint(t)})
	}
	for _, r := range g.Rules {
		addUnit("rule:"+r.Name, &unit{kind: "rule", name: r.Name, fp: ruleFingerprint(r)})
		refs := map[string]bool{}
		for _, x := range references(r) {
			refs[x] = true
		}
		edges[r.Name] = refs
	}

	// Determine changed units (new, removed or edited).
	changed := map[string]bool{}
	for key, u := range cur {
		old, ok := e.units[key]
		if !ok || old.fp != u.fp {
			changed[key] = true
		}
	}
	for key := range e.units {
		if _, ok := cur[key]; !ok {
			changed[key] = true
		}
	}

	// Reverse-dependency closure over productions: editing rule X also
	// invalidates every rule that (transitively) references X.
	rev := map[string]map[string]bool{}
	for from, tos := range edges {
		for to := range tos {
			if rev[to] == nil {
				rev[to] = map[string]bool{}
			}
			rev[to][from] = true
		}
	}
	affected := map[string]bool{}
	var stack []string
	for key := range changed {
		affected[key] = true
		if strings.HasPrefix(key, "rule:") {
			stack = append(stack, strings.TrimPrefix(key, "rule:"))
		}
	}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for dep := range rev[x] {
			key := "rule:" + dep
			if !affected[key] {
				affected[key] = true
				stack = append(stack, dep)
			}
		}
	}

	// First pass after construction: everything is recomputed.
	if !e.hasPrev {
		affected = map[string]bool{}
		for key := range cur {
			affected[key] = true
		}
	}

	// Recompute (validate) affected units, reusing cached errors elsewhere.
	var errs []error
	for key, u := range cur {
		if affected[key] {
			u.err = validateUnit(g, u)
			if u.err != nil {
				errs = append(errs, u.err)
			}
			continue
		}
		if old := e.units[key]; old != nil && old.err != nil {
			errs = append(errs, old.err)
		}
	}
	// Cheap grammar-wide checks that do not scale with grammar size.
	if g.Start == "" {
		errs = append(errs, fmt.Errorf("start rule must not be empty"))
	} else {
		found := false
		for _, r := range g.Rules {
			if r.Name == g.Start {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, fmt.Errorf("start rule %q is not declared", g.Start))
		}
	}

	recomputed := make([]string, 0, len(affected))
	for key := range affected {
		if _, ok := cur[key]; !ok {
			continue // removed units need no recomputation in this grammar
		}
		recomputed = append(recomputed, key)
	}
	sort.Strings(recomputed)

	rep := &Report{
		Valid:       len(errs) == 0,
		Errors:      errs,
		Recomputed:  recomputed,
		RecomputedN: len(recomputed),
		TotalUnits:  len(cur),
	}

	e.units = cur
	e.edges = edges
	e.revEdges = rev
	e.hasPrev = true
	return rep, nil
}

// AffectedRules returns the production names that would be revalidated if the
// named rules changed (reverse-dependency closure), without mutating state.
func (e *Engine) AffectedRules(g *grammar.Grammar, changedRule string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]bool{changedRule: true}
	rev := map[string]map[string]bool{}
	for _, r := range g.Rules {
		for _, x := range references(r) {
			if rev[x] == nil {
				rev[x] = map[string]bool{}
			}
			rev[x][r.Name] = true
		}
	}
	stack := []string{changedRule}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for dep := range rev[x] {
			if !out[dep] {
				out[dep] = true
				stack = append(stack, dep)
			}
		}
	}
	names := make([]string, 0, len(out))
	for k := range out {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func validateUnit(g *grammar.Grammar, u *unit) error {
	switch u.kind {
	case "token":
		for _, t := range g.Tokens {
			if t.Name == u.name {
				if t.Pattern == "" {
					return fmt.Errorf("token rule %q: pattern must not be empty", u.name)
				}
				return nil
			}
		}
	case "rule":
		for _, r := range g.Rules {
			if r.Name == u.name {
				if len(r.Alts) == 0 {
					return fmt.Errorf("production rule %q must have at least one alternative", u.name)
				}
				tokenNames := map[string]bool{}
				for _, t := range g.Tokens {
					tokenNames[t.Name] = true
				}
				ruleNames := map[string]bool{}
				for _, x := range g.Rules {
					ruleNames[x.Name] = true
				}
				var bad string
				var walk func(e grammar.Expr) bool
				walk = func(e grammar.Expr) bool {
					for _, t := range e.Terms {
						switch t.Kind {
						case grammar.TermTerminal:
							if !tokenNames[t.Name] {
								bad = fmt.Sprintf("unknown terminal %q", t.Name)
								return true
							}
						case grammar.TermNonTerminal:
							if !ruleNames[t.Name] {
								bad = fmt.Sprintf("unknown non-terminal %q", t.Name)
								return true
							}
						default:
							if t.Child != nil && walk(*t.Child) {
								return true
							}
						}
					}
					return false
				}
				for _, a := range r.Alts {
					if walk(a) {
						return fmt.Errorf("production rule %q: %s", u.name, bad)
					}
				}
				return nil
			}
		}
	}
	return nil
}

// FullBuildUnits reports the work a full rebuild would perform for g (every
// declaration unit).
func (e *Engine) FullBuildUnits(g *grammar.Grammar) int {
	return len(g.Tokens) + len(g.Rules)
}
