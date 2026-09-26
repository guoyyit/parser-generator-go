package grammar

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DefinitionError describes an invalid grammar declaration (duplicate or
// conflicting rules, dangling references, invalid patterns, etc.).
type DefinitionError struct {
	Pos     Position
	Message string
}

func (e *DefinitionError) Error() string {
	if e.Pos.Line > 0 || e.Pos.File != "" {
		return e.Pos.String() + ": " + e.Message
	}
	return e.Message
}

type validator struct {
	errs       []error
	tokenNames map[string]Position
	ruleNames  map[string]Position
}

// Validate performs structural validation of the grammar definition.
// Duplicate or conflicting declarations, dangling references and unusable
// lexical patterns are rejected explicitly rather than ignored.
func (g *Grammar) Validate() error {
	v := &validator{
		tokenNames: map[string]Position{},
		ruleNames:  map[string]Position{},
	}
	if strings.TrimSpace(g.Name) == "" {
		v.err("grammar name must not be empty", Position{})
	}
	if strings.TrimSpace(g.Version) == "" {
		v.err("grammar version must not be empty", Position{})
	} else if _, _, err := ParseVersion(g.Version); err != nil {
		v.err("grammar version: "+err.Error(), Position{})
	}

	// Lexical rules: unique names, valid non-empty-matching patterns.
	for _, t := range g.Tokens {
		if t.Name == "" {
			v.err("token rule name must not be empty", t.Pos)
			continue
		}
		if t.Pattern == "" {
			v.err(fmt.Sprintf("token rule %q: pattern must not be empty", t.Name), t.Pos)
		} else {
			re, err := regexp.Compile(t.Pattern)
			if err != nil {
				v.err(fmt.Sprintf("token rule %q: invalid pattern: %v", t.Name, err), t.Pos)
			} else {
				// Anchored, as the lexer uses it: a rule matching the empty
				// string would make tokenization undefined.
				anchored := regexp.MustCompile(`\A(?:` + t.Pattern + `)`)
				loc := anchored.FindStringIndex("")
				if loc != nil && loc[1]-loc[0] == 0 {
					v.err(fmt.Sprintf("token rule %q: pattern must not match the empty string", t.Name), t.Pos)
				}
				_ = re
			}
		}
		if prev, dup := v.tokenNames[t.Name]; dup {
			v.err(fmt.Sprintf("duplicate token rule %q (previously declared at %s)", t.Name, prev), t.Pos)
		} else {
			v.tokenNames[t.Name] = t.Pos
		}
	}

	// Production rules: unique names, non-empty alternative lists.
	for _, r := range g.Rules {
		if r.Name == "" {
			v.err("production rule name must not be empty", r.Pos)
			continue
		}
		if len(r.Alts) == 0 {
			v.err(fmt.Sprintf("production rule %q must have at least one alternative", r.Name), r.Pos)
		}
		if prev, dup := v.ruleNames[r.Name]; dup {
			v.err(fmt.Sprintf("duplicate production rule %q (previously declared at %s)", r.Name, prev), r.Pos)
		} else {
			v.ruleNames[r.Name] = r.Pos
		}
		if _, clash := v.tokenNames[r.Name]; clash {
			v.err(fmt.Sprintf("production rule %q clashes with a token rule of the same name", r.Name), r.Pos)
		}
	}

	if g.Start == "" {
		v.err("start rule must not be empty", Position{})
	} else if _, ok := v.ruleNames[g.Start]; !ok {
		v.err(fmt.Sprintf("start rule %q is not declared", g.Start), Position{})
	}

	// Reference checks, including inside optional/repeat/group wrappers.
	seen := map[string]bool{}
	for _, r := range g.Rules {
		for ai, a := range r.Alts {
			v.checkExpr(r.Name, ai, a, seen)
		}
	}

	return errors.Join(v.errs...)
}

func (v *validator) checkExpr(ruleName string, alt int, e Expr, seen map[string]bool) {
	for _, t := range e.Terms {
		switch t.Kind {
		case TermTerminal:
			if _, ok := v.tokenNames[t.Name]; !ok {
				v.err(fmt.Sprintf("rule %q alternative %d: unknown terminal %q", ruleName, alt+1, t.Name), t.Pos)
			}
		case TermNonTerminal:
			if _, ok := v.ruleNames[t.Name]; !ok {
				v.err(fmt.Sprintf("rule %q alternative %d: unknown non-terminal %q", ruleName, alt+1, t.Name), t.Pos)
			}
		case TermOptional, TermStar, TermPlus, TermGroup:
			if t.Child == nil {
				v.err(fmt.Sprintf("rule %q alternative %d: %s wrapper is missing its expression", ruleName, alt+1, kindName(t.Kind)), t.Pos)
			} else {
				v.checkExpr(ruleName, alt, *t.Child, seen)
			}
		default:
			v.err(fmt.Sprintf("rule %q alternative %d: malformed term", ruleName, alt+1), t.Pos)
		}
	}
}

func kindName(k TermKind) string {
	switch k {
	case TermOptional:
		return "optional"
	case TermStar:
		return "star"
	case TermPlus:
		return "plus"
	case TermGroup:
		return "group"
	default:
		return "unknown"
	}
}

func (v *validator) err(msg string, pos Position) {
	v.errs = append(v.errs, &DefinitionError{Pos: pos, Message: msg})
}

// ParseVersion parses a grammar version in MAJOR.MINOR.PATCH form.
func ParseVersion(s string) (major, minor int, err error) {
	var patch int
	n, perr := fmt.Sscanf(s, "%d.%d.%d", &major, &minor, &patch)
	if perr != nil || n != 3 {
		return 0, 0, fmt.Errorf("invalid version %q: want MAJOR.MINOR.PATCH", s)
	}
	if major < 0 || minor < 0 || patch < 0 {
		return 0, 0, fmt.Errorf("invalid version %q: components must be non-negative", s)
	}
	return major, minor, nil
}
