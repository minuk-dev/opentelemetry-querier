package lokiacceptor

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

var (
	errLogQLSyntax   = errors.New("lokiacceptor: invalid LogQL query")
	errLogQLDuration = errors.New("lokiacceptor: invalid range duration")
	errLogQLEmptySel = errors.New("lokiacceptor: a stream selector needs at least one matcher")
)

// LogQL range-duration units Go's time.ParseDuration does not cover.
const (
	oneDay  = 24 * time.Hour
	oneWeek = 7 * oneDay
)

// parseLogQL parses a subset of LogQL into a qdata QueryPlan over logs: a stream
// selector `{a="b", c=~"re"}` with optional line filters (|= |~ != !~), an
// optional range aggregation (rate/count_over_time/... over `[dur]`), and an
// optional vector aggregation (sum/avg/topk/... with by/without). Constructs
// outside the subset (label pipelines like `| json`, unwrap, parsers) return an
// error so the acceptor rejects them with 400.
func parseLogQL(input string) (*qdata.QueryPlan, error) {
	parser := &logqlParser{tokens: tokenizeLogQL(input), pos: 0}

	node, err := parser.parseExpr()
	if err != nil {
		return nil, err
	}

	if !parser.atEnd() {
		return nil, fmt.Errorf("%w: unexpected %q", errLogQLSyntax, parser.peek().value)
	}

	return qdata.Plan(node), nil
}

// ---- tokenizer ----

type logqlKind int

const (
	logqlIdent logqlKind = iota
	logqlString
	logqlNumber
	logqlLBrace
	logqlRBrace
	logqlLParen
	logqlRParen
	logqlLBracket
	logqlRBracket
	logqlComma
	logqlMatchOp // = != =~ !~
	logqlLineOp  // |= |~ (!= and !~ double as line ops by context)
	logqlEOF
)

type logqlToken struct {
	kind  logqlKind
	value string
}

func tokenizeLogQL(input string) []logqlToken {
	var tokens []logqlToken

	runes := []rune(input)
	for pos := 0; pos < len(runes); {
		char := runes[pos]

		switch {
		case unicode.IsSpace(char):
			pos++
		case char == '"' || char == '`':
			value, next := scanString(runes, pos)
			tokens = append(tokens, logqlToken{kind: logqlString, value: value})
			pos = next
		case strings.ContainsRune("{}()[],", char):
			tokens = append(tokens, logqlToken{kind: bracketKind(char), value: string(char)})
			pos++
		case strings.ContainsRune("=!|~", char):
			token, next := scanOperator(runes, pos)
			tokens = append(tokens, token)
			pos = next
		case unicode.IsDigit(char):
			value, next := scanNumberOrDuration(runes, pos)
			tokens = append(tokens, logqlToken{kind: logqlNumber, value: value})
			pos = next
		default:
			value, next := scanWhile(runes, pos, isIdentRune)
			if next == pos {
				// An unexpected character (scanWhile made no progress): emit it as a
				// single-rune ident so the parser rejects it, and always advance to
				// avoid an infinite loop.
				value, next = string(char), pos+1
			}

			tokens = append(tokens, logqlToken{kind: logqlIdent, value: value})
			pos = next
		}
	}

	return append(tokens, logqlToken{kind: logqlEOF, value: ""})
}

func bracketKind(char rune) logqlKind {
	switch char {
	case '{':
		return logqlLBrace
	case '}':
		return logqlRBrace
	case '(':
		return logqlLParen
	case ')':
		return logqlRParen
	case '[':
		return logqlLBracket
	case ']':
		return logqlRBracket
	default:
		return logqlComma
	}
}

// scanOperator reads a matcher/line operator: =, !=, =~, !~, |=, |~.
func scanOperator(runes []rune, start int) (logqlToken, int) {
	twoChar := ""
	if start+1 < len(runes) {
		twoChar = string(runes[start : start+2])
	}

	switch twoChar {
	case "=~", "!~", "!=":
		return logqlToken{kind: logqlMatchOp, value: twoChar}, start + len(twoChar)
	case "|=", "|~":
		return logqlToken{kind: logqlLineOp, value: twoChar}, start + len(twoChar)
	}

	if runes[start] == '=' {
		return logqlToken{kind: logqlMatchOp, value: "="}, start + 1
	}

	// A lone '!' or '|' is not valid on its own; surface it as an ident so the
	// parser rejects it in context.
	return logqlToken{kind: logqlIdent, value: string(runes[start])}, start + 1
}

func scanString(runes []rune, start int) (string, int) {
	quote := runes[start]

	var builder strings.Builder

	pos := start + 1
	for pos < len(runes) {
		if runes[pos] == '\\' && pos+1 < len(runes) && quote == '"' {
			builder.WriteRune(runes[pos+1])
			pos += 2

			continue
		}

		if runes[pos] == quote {
			return builder.String(), pos + 1
		}

		builder.WriteRune(runes[pos])
		pos++
	}

	return builder.String(), pos
}

// scanNumberOrDuration scans a bare number (topk param) or a range duration
// (e.g. 5m, 1h30m): the leading digits/dot plus any trailing duration units, so
// "5m" is one token rather than a number followed by an identifier.
func scanNumberOrDuration(runes []rune, start int) (string, int) {
	pos := start
	for pos < len(runes) && (unicode.IsDigit(runes[pos]) || runes[pos] == '.') {
		pos++
	}

	// A trailing unit suffix (letters, and digits for compound durations).
	for pos < len(runes) && (unicode.IsLetter(runes[pos]) || unicode.IsDigit(runes[pos])) {
		pos++
	}

	return string(runes[start:pos]), pos
}

func scanWhile(runes []rune, start int, keep func(rune) bool) (string, int) {
	pos := start
	for pos < len(runes) && keep(runes[pos]) {
		pos++
	}

	return string(runes[start:pos]), pos
}

func isIdentRune(char rune) bool {
	return unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_'
}

// ---- parser ----

type logqlParser struct {
	tokens []logqlToken
	pos    int
}

func (p *logqlParser) peek() logqlToken { return p.tokens[p.pos] }
func (p *logqlParser) atEnd() bool      { return p.peek().kind == logqlEOF }

func (p *logqlParser) next() logqlToken {
	token := p.tokens[p.pos]
	if token.kind != logqlEOF {
		p.pos++
	}

	return token
}

func (p *logqlParser) expect(kind logqlKind, what string) error {
	if p.peek().kind != kind {
		return fmt.Errorf("%w: expected %s, got %q", errLogQLSyntax, what, p.peek().value)
	}

	p.next()

	return nil
}

func (p *logqlParser) parseExpr() (*qdata.Node, error) {
	token := p.peek()

	if token.kind == logqlLBrace {
		return p.parseLogSelector()
	}

	if token.kind == logqlIdent {
		if _, ok := rangeAggOp(token.value); ok {
			return p.parseRangeAgg()
		}

		if _, ok := vectorAggOp(token.value); ok {
			return p.parseVectorAgg()
		}
	}

	return nil, fmt.Errorf("%w: unexpected %q", errLogQLSyntax, token.value)
}

func (p *logqlParser) parseVectorAgg() (*qdata.Node, error) {
	operator, _ := vectorAggOp(p.next().value)

	byLabels, withoutLabels, err := p.parseGroupingOpt()
	if err != nil {
		return nil, err
	}

	err = p.expect(logqlLParen, "(")
	if err != nil {
		return nil, err
	}

	param, err := p.parseAggParam(operator)
	if err != nil {
		return nil, err
	}

	inner, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	// A vector aggregation operates on a metric query (a range aggregation or a
	// nested vector aggregation), never directly on a raw log stream — sum({...})
	// is invalid LogQL. Reject it here rather than let the backend 400.
	if inner.GetSelect() != nil {
		return nil, fmt.Errorf("%w: vector aggregation requires a metric query, not a raw stream", errLogQLSyntax)
	}

	err = p.expect(logqlRParen, ")")
	if err != nil {
		return nil, err
	}

	// Grouping may also appear after the parentheses.
	if byLabels == nil && withoutLabels == nil {
		byLabels, withoutLabels, err = p.parseGroupingOpt()
		if err != nil {
			return nil, err
		}
	}

	return qdata.AggregateNode(operator, byLabels, withoutLabels, param, inner), nil
}

// parseAggParam consumes a leading scalar parameter (topk/bottomk `k`) plus its
// trailing comma, returning 0 when the op takes no parameter.
func (p *logqlParser) parseAggParam(operator qdata.AggOp) (float64, error) {
	if !aggTakesParam(operator) || p.peek().kind != logqlNumber {
		return 0, nil
	}

	param, err := strconv.ParseFloat(p.next().value, floatBitSize)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid aggregation parameter", errLogQLSyntax)
	}

	return param, p.expect(logqlComma, ",")
}

func (p *logqlParser) parseRangeAgg() (*qdata.Node, error) {
	operator, _ := rangeAggOp(p.next().value)

	err := p.expect(logqlLParen, "(")
	if err != nil {
		return nil, err
	}

	selector, err := p.parseLogSelector()
	if err != nil {
		return nil, err
	}

	err = p.expect(logqlLBracket, "[")
	if err != nil {
		return nil, err
	}

	if p.peek().kind != logqlIdent && p.peek().kind != logqlNumber {
		return nil, fmt.Errorf("%w: expected a duration", errLogQLDuration)
	}

	window, err := parseLogQLDuration(p.next().value)
	if err != nil {
		return nil, err
	}

	err = p.expect(logqlRBracket, "]")
	if err != nil {
		return nil, err
	}

	err = p.expect(logqlRParen, ")")
	if err != nil {
		return nil, err
	}

	return qdata.TimeAggNode(operator, window, selector), nil
}

func (p *logqlParser) parseLogSelector() (*qdata.Node, error) {
	err := p.expect(logqlLBrace, "{")
	if err != nil {
		return nil, err
	}

	leaves, err := p.parseMatchers()
	if err != nil {
		return nil, err
	}

	err = p.expect(logqlRBrace, "}")
	if err != nil {
		return nil, err
	}

	if len(leaves) == 0 {
		return nil, errLogQLEmptySel
	}

	lines, err := p.parseLineFilters()
	if err != nil {
		return nil, err
	}

	filter := leaves[0]
	if len(leaves) > 1 {
		filter = qdata.BoolPredicate(qdata.BoolAnd, leaves...)
	}

	return qdata.SelectNode(qdata.SignalLogs, filter, lines...), nil
}

func (p *logqlParser) parseMatchers() ([]*qdata.Predicate, error) {
	var leaves []*qdata.Predicate

	for p.peek().kind == logqlIdent {
		name := p.next().value

		if p.peek().kind != logqlMatchOp {
			return nil, fmt.Errorf("%w: expected a matcher operator after %q", errLogQLSyntax, name)
		}

		operator, err := matchOp(p.next().value)
		if err != nil {
			return nil, err
		}

		if p.peek().kind != logqlString {
			return nil, fmt.Errorf("%w: expected a quoted value for %q", errLogQLSyntax, name)
		}

		value := p.next().value
		leaves = append(leaves, qdata.LeafPredicate(&qdata.LabelMatcher{Name: name, Op: operator, Value: value}))

		// Matchers must be comma-separated; without a comma this matcher is the
		// last one, and parseLogSelector then requires the closing brace (a
		// following identifier without a comma surfaces as a syntax error there).
		if p.peek().kind != logqlComma {
			break
		}

		p.next()
	}

	return leaves, nil
}

func (p *logqlParser) parseLineFilters() ([]*qdata.LineMatch, error) {
	var lines []*qdata.LineMatch

	for p.peek().kind == logqlLineOp || (p.peek().kind == logqlMatchOp && isLineNegation(p.peek().value)) {
		operator, err := lineOp(p.next().value)
		if err != nil {
			return nil, err
		}

		if p.peek().kind != logqlString {
			return nil, fmt.Errorf("%w: expected a quoted line filter value", errLogQLSyntax)
		}

		lines = append(lines, qdata.LineFilter(operator, p.next().value))
	}

	return lines, nil
}

// parseGroupingOpt parses an optional `by(...)`/`without(...)` clause, returning
// the by-labels, the without-labels, and an error. At most one list is non-nil.
func (p *logqlParser) parseGroupingOpt() ([]string, []string, error) {
	token := p.peek()
	if token.kind != logqlIdent || (token.value != "by" && token.value != "without") {
		return nil, nil, nil
	}

	p.next()

	err := p.expect(logqlLParen, "(")
	if err != nil {
		return nil, nil, err
	}

	var labels []string
	for p.peek().kind == logqlIdent {
		labels = append(labels, p.next().value)

		if p.peek().kind == logqlComma {
			p.next()
		}
	}

	err = p.expect(logqlRParen, ")")
	if err != nil {
		return nil, nil, err
	}

	if token.value == "without" {
		return nil, labels, nil
	}

	return labels, nil, nil
}

// ---- operator & literal mapping ----

func matchOp(symbol string) (qdata.MatchOp, error) {
	switch symbol {
	case "=":
		return qdata.MatchEqual, nil
	case "!=":
		return qdata.MatchNotEqual, nil
	case "=~":
		return qdata.MatchRegexp, nil
	case "!~":
		return qdata.MatchNotRegexp, nil
	default:
		return qdata.MatchEqual, fmt.Errorf("%w: unknown matcher %q", errLogQLSyntax, symbol)
	}
}

// lineOp maps a LogQL line-filter operator to a MatchOp: |= contains, |~ regexp,
// and the shared != / !~ negate them.
func lineOp(symbol string) (qdata.MatchOp, error) {
	switch symbol {
	case "|=":
		return qdata.MatchEqual, nil
	case "!=":
		return qdata.MatchNotEqual, nil
	case "|~":
		return qdata.MatchRegexp, nil
	case "!~":
		return qdata.MatchNotRegexp, nil
	default:
		return qdata.MatchEqual, fmt.Errorf("%w: unknown line filter %q", errLogQLSyntax, symbol)
	}
}

// isLineNegation reports whether a matcher-op token (!= / !~) is acting as a
// negated line filter after the stream selector.
func isLineNegation(symbol string) bool { return symbol == "!=" || symbol == "!~" }

// rangeAggOp maps a LogQL log-range aggregation name to a TimeAggOp.
func rangeAggOp(name string) (qdata.TimeAggOp, bool) {
	operator, ok := map[string]qdata.TimeAggOp{
		"rate":            qdata.TimeAggRate,
		"count_over_time": qdata.TimeAggCountOverTime,
		"sum_over_time":   qdata.TimeAggSumOverTime,
		"avg_over_time":   qdata.TimeAggAvgOverTime,
		"min_over_time":   qdata.TimeAggMinOverTime,
		"max_over_time":   qdata.TimeAggMaxOverTime,
	}[name]

	return operator, ok
}

// vectorAggOp maps a LogQL vector-aggregation name to an AggOp.
func vectorAggOp(name string) (qdata.AggOp, bool) {
	operator, ok := map[string]qdata.AggOp{
		"sum":     qdata.AggSum,
		"avg":     qdata.AggAvg,
		"min":     qdata.AggMin,
		"max":     qdata.AggMax,
		"count":   qdata.AggCount,
		"stddev":  qdata.AggStddev,
		"stdvar":  qdata.AggStdvar,
		"topk":    qdata.AggTopK,
		"bottomk": qdata.AggBottomK,
	}[name]

	return operator, ok
}

// aggTakesParam reports whether a vector-aggregation op takes a leading scalar
// parameter (topk/bottomk in LogQL).
func aggTakesParam(op qdata.AggOp) bool {
	return op == qdata.AggTopK || op == qdata.AggBottomK
}

// parseLogQLDuration parses a LogQL range duration. Go's time.ParseDuration
// covers s/m/h; LogQL also allows d (days) and w (weeks), handled here.
func parseLogQLDuration(raw string) (time.Duration, error) {
	if unit := raw[len(raw)-1]; unit == 'd' || unit == 'w' {
		count, err := strconv.ParseFloat(raw[:len(raw)-1], 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %q", errLogQLDuration, raw)
		}

		span := oneDay
		if unit == 'w' {
			span = oneWeek
		}

		return time.Duration(count * float64(span)), nil
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", errLogQLDuration, raw)
	}

	return parsed, nil
}
