// Package recovery defines configurable parse error recovery strategies.
package recovery

// Mode selects a recovery behavior.
type Mode int

const (
	// RecoverNone performs no recovery: parsing stops at the first error.
	RecoverNone Mode = iota
	// RecoverSyncTo synchronizes to the next token in the rule's follow set.
	RecoverSyncTo
	// RecoverSkipTokens skips unexpected tokens until parsing resumes.
	RecoverSkipTokens
)

// Strategy configures recovery behavior.
type Strategy struct {
	Mode           Mode
	MaxErrors      int
	FollowFallback []string // terminal names used when follow info is absent
}

// DefaultStrategy returns the default: no recovery.
func DefaultStrategy() Strategy { return Strategy{} }
