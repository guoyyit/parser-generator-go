package runtime

import "github.com/guoyyit/parser-generator-go/lexer"

// ParseTokensExport parses an explicit visible-token stream (test helper).
func (p *Parser) ParseTokensExport(toks []lexer.Token, input string) *Result {
	res, _ := p.parseTokens(input, toks, Options{})
	return res
}
