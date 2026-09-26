// Package service is the concurrency-safe facade over the parser generator
// and its runtime. A Service may be shared by goroutines; generated parsers
// are immutable and a single Parse call mutates no shared state.
package service

import (
	"errors"
	"fmt"
	"sync"

	"github.com/guoyyit/parser-generator-go/generator"
	"github.com/guoyyit/parser-generator-go/grammar"
	"github.com/guoyyit/parser-generator-go/runtime"
	"github.com/guoyyit/parser-generator-go/version"
)

// Service is a reusable generator and registry of compiled grammars.
type Service struct {
	mu    sync.RWMutex
	gen   *generator.Generator
	store map[string]*generator.CompiledGrammar
}

// New creates a Service.
func New() *Service {
	return &Service{gen: generator.New(), store: map[string]*generator.CompiledGrammar{}}
}

// Generate compiles a grammar definition and registers it under its name.
// Registering replaces any previously compiled grammar of the same name;
// parsers already handed out remain usable (they are immutable).
func (s *Service) Generate(def *grammar.Grammar) (*generator.CompiledGrammar, error) {
	if def == nil {
		return nil, errors.New("service: nil grammar definition")
	}
	cg, err := s.gen.Generate(def)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.store[def.Name] = cg
	s.mu.Unlock()
	return cg, nil
}

// Get returns the compiled grammar registered under name.
func (s *Service) Get(name string) (*generator.CompiledGrammar, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cg, ok := s.store[name]
	return cg, ok
}

// Parse parses input using the registered parser for the given grammar name.
// It is safe to call concurrently for the same grammar.
func (s *Service) Parse(name, input string) (*runtime.Result, error) {
	s.mu.RLock()
	cg, ok := s.store[name]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("service: grammar %q is not registered", name)
	}
	return cg.Parser.Parse(input)
}

// ParseWith parses input with per-call options (e.g. a recovery strategy).
// Options are call-scoped and never mutate the shared parser.
func (s *Service) ParseWith(name, input string, opts runtime.Options) (*runtime.Result, error) {
	s.mu.RLock()
	cg, ok := s.store[name]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("service: grammar %q is not registered", name)
	}
	return cg.Parser.ParseWith(input, opts)
}

// Compatibility checks an existing artifact's version and signature against a
// possibly newer definition, reporting every mismatch reason.
func (s *Service) Compatibility(def *grammar.Grammar, artifactVersion, artifactSignature string) version.CompatibilityResult {
	return version.Check(def, def.Name, artifactVersion, artifactSignature)
}
