// Package version provides grammar content signatures and compatibility
// checks between grammar definitions and previously generated artifacts.
//
// Compatibility is decided on two axes:
//
//   - Content signature: a stable SHA-256 over the normalized definition.
//     A mismatch means the artifact was generated from different content.
//   - Semantic version: the grammar's MAJOR.MINOR.PATCH declares the intended
//     compatibility. A different major version is always incompatible; a
//     different minor version of an older artifact is reported as a reason.
package version

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/guoyyit/parser-generator-go/grammar"
)

// Mismatch describes why a definition is incompatible with an artifact.
type Mismatch struct {
	Reason string
	Detail string
}

func (m *Mismatch) Error() string { return m.Reason + ": " + m.Detail }

// CompatibilityResult summarizes a compatibility check.
type CompatibilityResult struct {
	Compatible bool
	Mismatches []Mismatch
}

func (r *CompatibilityResult) Error() string {
	if len(r.Mismatches) == 0 {
		return "compatible"
	}
	parts := make([]string, len(r.Mismatches))
	for i, m := range r.Mismatches {
		parts[i] = m.Error()
	}
	return strings.Join(parts, "; ")
}

// Signature computes a stable content signature for a grammar definition.
// The serialization is canonical (sorted, position-independent) so that two
// definitions with identical rules produce identical signatures.
func Signature(g *grammar.Grammar) string {
	h := sha256.New()
	fmt.Fprintf(h, "grammar:%s\n", g.Name)
	fmt.Fprintf(h, "start:%s\n", g.Start)

	tokens := append([]grammar.TokenRule(nil), g.Tokens...)
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].Name < tokens[j].Name })
	for _, t := range tokens {
		fmt.Fprintf(h, "token:%s\t%d\t%v\t%s\n", t.Name, t.Priority, t.Ignore, t.Pattern)
	}

	rules := append([]grammar.Rule(nil), g.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].Name < rules[j].Name })
	for _, r := range rules {
		fmt.Fprintf(h, "rule:%s\n", r.Name)
		for ai, a := range r.Alts {
			fmt.Fprintf(h, " alt:%d:", ai)
			for _, t := range a.Terms {
				fmt.Fprintf(h, " %s(%s)%s", kindTag(t.Kind), t.Name, childSig(t.Child))
			}
			h.Write([]byte{'\n'})
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func childSig(e *grammar.Expr) string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("[")
	for i, t := range e.Terms {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%s(%s)%s", kindTag(t.Kind), t.Name, childSig(t.Child))
	}
	b.WriteString("]")
	return b.String()
}

func kindTag(k grammar.TermKind) string {
	switch k {
	case grammar.TermTerminal:
		return "T"
	case grammar.TermNonTerminal:
		return "N"
	case grammar.TermOptional:
		return "O"
	case grammar.TermStar:
		return "*"
	case grammar.TermPlus:
		return "+"
	case grammar.TermGroup:
		return "G"
	default:
		return "?"
	}
}

// Check verifies that an artifact (identified by name/version/signature) is
// compatible with a grammar definition. Every reason for incompatibility is
// reported rather than failing at the first one.
func Check(g *grammar.Grammar, artifactName, artifactVersion, artifactSignature string) CompatibilityResult {
	var out []Mismatch
	if g.Name != artifactName {
		out = append(out, Mismatch{
			Reason: "grammar name mismatch",
			Detail: fmt.Sprintf("artifact belongs to %q but the definition is %q", artifactName, g.Name),
		})
	}
	curMajor, curMinor, verr := grammar.ParseVersion(g.Version)
	artMajor, artMinor, aerr := grammar.ParseVersion(artifactVersion)
	switch {
	case verr != nil:
		out = append(out, Mismatch{Reason: "invalid current version", Detail: verr.Error()})
	case aerr != nil:
		out = append(out, Mismatch{Reason: "invalid artifact version", Detail: aerr.Error()})
	default:
		if curMajor != artMajor {
			out = append(out, Mismatch{
				Reason: "incompatible major version",
				Detail: fmt.Sprintf("artifact %s vs definition %s: major version changed; artifacts must be regenerated",
					artifactVersion, g.Version),
			})
		} else if curMinor != artMinor {
			out = append(out, Mismatch{
				Reason: "minor version differs",
				Detail: fmt.Sprintf("artifact %s vs definition %s: regenerate the artifact",
					artifactVersion, g.Version),
			})
		}
	}
	if sig := Signature(g); sig != artifactSignature {
		out = append(out, Mismatch{
			Reason: "content signature mismatch",
			Detail: fmt.Sprintf("artifact %s was generated from different content than the current definition (%s)",
				shortSig(artifactSignature), shortSig(sig)),
		})
	}
	return CompatibilityResult{Compatible: len(out) == 0, Mismatches: out}
}

func shortSig(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "<none>"
	}
	return s
}
