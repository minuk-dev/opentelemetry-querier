package elasticsearchacceptor

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// defaultField is the field a bare (unqualified) Lucene term matches against.
const defaultField = "message"

var (
	errLuceneSyntax     = errors.New("elasticsearchacceptor: invalid Lucene query")
	errLuceneTrailing   = errors.New("elasticsearchacceptor: unexpected trailing input")
	errLuceneUnbalanced = errors.New("elasticsearchacceptor: unbalanced parentheses")
)

// parseLucene parses a subset of the Lucene query syntax into a qdata QueryPlan
// over logs. The subset covers field:value and bare terms, quoted phrases,
// AND/OR/NOT (and the `-`/`+` prefixes), and parenthesised grouping — mapped
// onto the predicate tree (AND/OR/NOT + equality leaves). A lone `*` selects
// everything. Constructs outside the subset (ranges, wildcards, boosts) are
// treated as literal term values.
func parseLucene(input string) (*qdata.QueryPlan, error) {
	parser := &luceneParser{tokens: tokenizeLucene(input), pos: 0}

	// A lone match-all selects every document: a Select with no filter.
	if parser.matchAllOnly() {
		return qdata.Plan(qdata.SelectNode(qdata.SignalLogs, nil)), nil
	}

	pred, err := parser.parseOr()
	if err != nil {
		return nil, err
	}

	if !parser.atEnd() {
		return nil, fmt.Errorf("%w: near %q", errLuceneTrailing, parser.peek().value)
	}

	return qdata.Plan(qdata.SelectNode(qdata.SignalLogs, pred)), nil
}

// ---- tokenizer ----

type tokenKind int

const (
	tokenTerm tokenKind = iota
	tokenColon
	tokenLParen
	tokenRParen
	tokenAnd
	tokenOr
	tokenNot
	tokenEOF
)

type luceneToken struct {
	kind   tokenKind
	value  string
	quoted bool
}

func tokenizeLucene(input string) []luceneToken {
	var tokens []luceneToken

	runes := []rune(input)
	for pos := 0; pos < len(runes); {
		char := runes[pos]

		switch {
		case unicode.IsSpace(char), char == '+':
			// Whitespace separates; `+` is a required-term marker that the default
			// conjunction already implies.
			pos++
		case char == '"':
			value, next := scanQuoted(runes, pos)
			tokens = append(tokens, luceneToken{kind: tokenTerm, value: value, quoted: true})
			pos = next
		default:
			if punct, ok := punctToken(char); ok {
				tokens = append(tokens, punct)
				pos++

				continue
			}

			value, next := scanBareword(runes, pos)
			tokens = append(tokens, keywordToken(value))
			pos = next
		}
	}

	return append(tokens, luceneToken{kind: tokenEOF, value: "", quoted: false})
}

// punctToken maps a single punctuation rune to its token.
func punctToken(char rune) (luceneToken, bool) {
	switch char {
	case ':':
		return luceneToken{kind: tokenColon, value: ":", quoted: false}, true
	case '(':
		return luceneToken{kind: tokenLParen, value: "(", quoted: false}, true
	case ')':
		return luceneToken{kind: tokenRParen, value: ")", quoted: false}, true
	case '-', '!':
		return luceneToken{kind: tokenNot, value: string(char), quoted: false}, true
	default:
		return luceneToken{kind: tokenTerm, value: "", quoted: false}, false
	}
}

func scanQuoted(runes []rune, start int) (string, int) {
	var builder strings.Builder

	pos := start + 1
	for pos < len(runes) {
		if runes[pos] == '\\' && pos+1 < len(runes) {
			builder.WriteRune(runes[pos+1])
			pos += 2

			continue
		}

		if runes[pos] == '"' {
			return builder.String(), pos + 1
		}

		builder.WriteRune(runes[pos])
		pos++
	}

	return builder.String(), pos
}

func scanBareword(runes []rune, start int) (string, int) {
	pos := start
	for pos < len(runes) && !isBarewordBoundary(runes[pos]) {
		pos++
	}

	return string(runes[start:pos]), pos
}

func isBarewordBoundary(char rune) bool {
	return unicode.IsSpace(char) || char == ':' || char == '(' || char == ')' || char == '"'
}

func keywordToken(word string) luceneToken {
	switch strings.ToUpper(word) {
	case "AND", "&&":
		return luceneToken{kind: tokenAnd, value: word, quoted: false}
	case "OR", "||":
		return luceneToken{kind: tokenOr, value: word, quoted: false}
	case "NOT":
		return luceneToken{kind: tokenNot, value: word, quoted: false}
	default:
		return luceneToken{kind: tokenTerm, value: word, quoted: false}
	}
}

// ---- parser ----

type luceneParser struct {
	tokens []luceneToken
	pos    int
}

func (p *luceneParser) peek() luceneToken { return p.tokens[p.pos] }
func (p *luceneParser) atEnd() bool       { return p.peek().kind == tokenEOF }

func (p *luceneParser) next() luceneToken {
	token := p.tokens[p.pos]
	if token.kind != tokenEOF {
		p.pos++
	}

	return token
}

// matchAllOnly reports whether the whole query is a single unquoted `*`.
func (p *luceneParser) matchAllOnly() bool {
	return len(p.tokens) == 2 && p.tokens[0].kind == tokenTerm &&
		!p.tokens[0].quoted && p.tokens[0].value == "*"
}

func (p *luceneParser) parseOr() (*qdata.Predicate, error) {
	operands, err := p.parseOperandList(p.parseAnd, tokenOr)
	if err != nil {
		return nil, err
	}

	if len(operands) == 1 {
		return operands[0], nil
	}

	return qdata.BoolPredicate(qdata.BoolOr, operands...), nil
}

func (p *luceneParser) parseAnd() (*qdata.Predicate, error) {
	operands := []*qdata.Predicate{}

	for {
		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		operands = append(operands, operand)

		// AND is explicit (AND/&&) or implicit (juxtaposition): keep consuming
		// until an OR, a close paren, or the end.
		if p.peek().kind == tokenAnd {
			p.next()
		}

		if p.peek().kind == tokenOr || p.peek().kind == tokenRParen || p.atEnd() {
			break
		}
	}

	if len(operands) == 1 {
		return operands[0], nil
	}

	return qdata.BoolPredicate(qdata.BoolAnd, operands...), nil
}

func (p *luceneParser) parseNot() (*qdata.Predicate, error) {
	if p.peek().kind == tokenNot {
		p.next()

		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}

		return qdata.BoolPredicate(qdata.BoolNot, operand), nil
	}

	return p.parsePrimary()
}

func (p *luceneParser) parsePrimary() (*qdata.Predicate, error) {
	if p.peek().kind == tokenLParen {
		p.next()

		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}

		if p.peek().kind != tokenRParen {
			return nil, fmt.Errorf("%w: expected )", errLuceneUnbalanced)
		}

		p.next()

		return inner, nil
	}

	return p.parseClause()
}

// parseClause parses `field:value` or a bare `value` into an equality leaf.
func (p *luceneParser) parseClause() (*qdata.Predicate, error) {
	if p.peek().kind != tokenTerm {
		return nil, fmt.Errorf("%w: expected a term, got %q", errLuceneSyntax, p.peek().value)
	}

	first := p.next()

	// `field:value` — a term followed by a colon and a value.
	if p.peek().kind == tokenColon {
		p.next()

		if p.peek().kind != tokenTerm {
			return nil, fmt.Errorf("%w: expected a value after field %q", errLuceneSyntax, first.value)
		}

		value := p.next()

		return leafEqual(first.value, value.value), nil
	}

	// A bare term matches the default field.
	return leafEqual(defaultField, first.value), nil
}

// parseOperandList parses one-or-more sub-expressions separated by sep.
func (p *luceneParser) parseOperandList(
	parse func() (*qdata.Predicate, error),
	sep tokenKind,
) ([]*qdata.Predicate, error) {
	operands := []*qdata.Predicate{}

	for {
		operand, err := parse()
		if err != nil {
			return nil, err
		}

		operands = append(operands, operand)

		if p.peek().kind != sep {
			return operands, nil
		}

		p.next()
	}
}

func leafEqual(name, value string) *qdata.Predicate {
	return qdata.LeafPredicate(&qdata.LabelMatcher{Name: name, Op: qdata.MatchEqual, Value: value})
}
