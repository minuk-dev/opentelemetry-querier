package crosssignaldispatcher

import (
	"slices"
	"strconv"
	"strings"

	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Synthetic column names added when normalizing a single-signal Result to a
// relational Table.
const (
	metricNameColumn  = "__name__"
	metricValueColumn = "value"
	logBodyColumn     = "body"
	// shadowedLabelPrefix renames a series label that collides with a synthetic
	// column (e.g. a label literally named "value") so the label is preserved
	// while the synthetic column keeps its stable name.
	shadowedLabelPrefix = "label_"
	// rightPrefix disambiguates a right-side column whose name also exists on the
	// left and is not a join key.
	rightPrefix = "right_"
	// keyDelim separates a join key's column values into a tuple, chosen to not
	// appear in label values.
	keyDelim    = "\x00"
	floatFmt    = -1
	floatBit    = 64
	decimalBase = 10
)

// resultToTable normalizes a single-signal backend Result into a relational
// Table so results of different signals can be joined uniformly. Metrics become
// one row per series (labels + latest value), logs one row per record (attributes
// + body); a Table result passes through (with its schema completed). Other
// payloads fail closed. A range metrics series is reduced to its latest point; a
// side-channel warning records the truncation so it is not silent.
func resultToTable(signal qdata.Signal, result *qdata.Result) (*qdata.Table, error) {
	switch data := result.GetData().(type) {
	case *qdatav1.Result_Metrics:
		warnIfTruncated(result, data.Metrics)

		return rowsToTable(metricRows(data.Metrics)), nil
	case *qdatav1.Result_Logs:
		return rowsToTable(logRows(data.Logs)), nil
	case *qdatav1.Result_Table:
		return completeSchema(data.Table), nil
	default:
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignaldispatcher: cannot join signal %s result (unsupported payload)", signal)
	}
}

// warnIfTruncated records a side-channel notification when a series carries more
// than one point, since only the latest is kept in the relational form.
func warnIfTruncated(result *qdata.Result, metrics *qdata.Metrics) {
	for _, series := range metrics.GetSeries() {
		if len(series.GetPoints()) > 1 {
			qdata.Warn(result, "series_truncated",
				"cross-signal join keeps only each series' latest point", "crosssignaldispatcher")

			return
		}
	}
}

func metricRows(metrics *qdata.Metrics) []*qdata.Row {
	rows := make([]*qdata.Row, 0, len(metrics.GetSeries()))

	for _, series := range metrics.GetSeries() {
		kvl := cloneAttrs(series.GetAttributes())

		// A series that already carries a __name__ label keeps it; otherwise the
		// series name fills the synthetic column (never clobbering a real label).
		if name := series.GetName(); name != "" {
			if _, exists := qdata.AttrGet(kvl, metricNameColumn); !exists {
				qdata.AttrPut(kvl, metricNameColumn, qdata.Str(name))
			}
		}

		putSample(kvl, latestValue(series))
		rows = append(rows, &qdata.Row{Values: kvl})
	}

	return rows
}

// putSample stores the metric sample under the reserved value column. A series
// label literally named "value" would otherwise be clobbered, so it is moved to
// a shadowedLabelPrefix column first — both survive and the value column keeps a
// stable name for downstream readers.
func putSample(kvl *qdata.KeyValueList, sample *qdata.Value) {
	if label, ok := qdata.AttrGet(kvl, metricValueColumn); ok {
		qdata.AttrDelete(kvl, metricValueColumn)
		qdata.AttrPut(kvl, shadowedLabelPrefix+metricValueColumn, label)
	}

	qdata.AttrPut(kvl, metricValueColumn, sample)
}

func logRows(logs *qdata.Logs) []*qdata.Row {
	rows := make([]*qdata.Row, 0, len(logs.GetRecords()))

	for _, record := range logs.GetRecords() {
		kvl := cloneAttrs(record.GetAttributes())
		qdata.AttrPut(kvl, logBodyColumn, record.GetBody())
		rows = append(rows, &qdata.Row{Values: kvl})
	}

	return rows
}

// latestValue returns the last point's value of a series, or an unset Value when
// the series has no points. The relational join is instant-oriented: a range
// series is intentionally reduced to its latest point (warnIfTruncated flags it).
// A per-step representation is out of scope (issue #52).
func latestValue(series *qdata.MetricSeries) *qdata.Value {
	points := series.GetPoints()
	if len(points) == 0 {
		return &qdata.Value{}
	}

	return points[len(points)-1].GetValue()
}

func cloneAttrs(attrs *qdata.KeyValueList) *qdata.KeyValueList {
	out := &qdata.KeyValueList{}
	for _, kv := range attrs.GetValues() {
		qdata.AttrPut(out, kv.GetKey(), kv.GetValue())
	}

	return out
}

// rowsToTable derives the column schema as the union of the rows' keys, in first-
// seen order, so the Table is self-describing.
func rowsToTable(rows []*qdata.Row) *qdata.Table {
	return &qdata.Table{Columns: unionColumns(nil, rows), Rows: rows}
}

// completeSchema keeps a passthrough Table's declared columns but appends any row
// key missing from them, so the clash-detection invariant (schema ⊇ row keys)
// that mergeRow/mergedColumns rely on holds even for upstream-built tables.
func completeSchema(table *qdata.Table) *qdata.Table {
	return &qdata.Table{Columns: unionColumns(table.GetColumns(), table.GetRows()), Rows: table.GetRows()}
}

// unionColumns returns declared columns (in order) plus any row key not already
// present, in first-seen order.
func unionColumns(declared []string, rows []*qdata.Row) []string {
	columns := slices.Clone(declared)
	seen := toSet(declared)

	for _, row := range rows {
		for _, kv := range row.GetValues().GetValues() {
			if _, ok := seen[kv.GetKey()]; !ok {
				seen[kv.GetKey()] = struct{}{}
				columns = append(columns, kv.GetKey())
			}
		}
	}

	return columns
}

// joinMode is how a BinaryOp combines the two sides relationally.
type joinMode int

const (
	joinInner joinMode = iota // AND: rows present on both sides, columns merged.
	joinAnti                  // UNLESS: left rows with no matching right row.
)

// joinModeFor maps a cross-signal BinaryOp operator to a relational join mode.
// Only the set operators AND/UNLESS have a well-defined relational meaning here;
// arithmetic, comparison and OR (union of heterogeneous schemas) fail closed
// rather than silently degrading to an inner join.
func joinModeFor(operator qdatav1.BinOp) (joinMode, error) {
	mode, ok := map[qdatav1.BinOp]joinMode{
		qdatav1.BinOp_BIN_OP_AND:    joinInner,
		qdatav1.BinOp_BIN_OP_UNLESS: joinAnti,
	}[operator]
	if !ok {
		return joinInner, qerror.New(qerror.CodeInvalidArgument,
			"crosssignaldispatcher: cross-signal operator %s is not a supported join (use AND or UNLESS)", operator)
	}

	return mode, nil
}

// joinTables joins two normalized tables per the BinaryOp's matching and mode.
func joinTables(left, right *qdata.Table, matching *qdatav1.VectorMatch, mode joinMode) (*qdata.Table, error) {
	keys, err := resolveKeys(left, right, matching)
	if err != nil {
		return nil, err
	}

	index := indexRows(right.GetRows(), keys)

	if mode == joinAnti {
		return antiJoin(left, keys, index), nil
	}

	return innerJoin(left, right, keys, index), nil
}

// resolveKeys picks the equijoin columns: the explicit `on` list; else the shared
// columns minus any `ignoring` labels; failing closed when nothing usable remains
// (a cross product of metrics × logs is meaningless).
func resolveKeys(left, right *qdata.Table, matching *qdatav1.VectorMatch) ([]string, error) {
	if on := matching.GetOn(); len(on) > 0 {
		return on, nil
	}

	keys := sharedColumns(left, right)
	if ignoring := matching.GetIgnoring(); len(ignoring) > 0 {
		keys = removeAll(keys, ignoring)
	}

	if len(keys) == 0 {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignaldispatcher: no join key (sides share no columns after `ignoring`, and no `on` was given)")
	}

	return keys, nil
}

func innerJoin(left, right *qdata.Table, keys []string, index map[string][]*qdata.Row) *qdata.Table {
	joined := &qdata.Table{Columns: mergedColumns(left, right, keys)}

	for _, lrow := range left.GetRows() {
		tuple, ok := keyTuple(rowMap(lrow), keys)
		if !ok {
			continue
		}

		for _, rrow := range index[tuple] {
			joined.Rows = append(joined.Rows, mergeRow(lrow, rrow, left.GetColumns(), keys))
		}
	}

	return joined
}

// antiJoin keeps left rows with no matching right row (UNLESS). A left row that
// lacks a join key can never match, so it is kept.
func antiJoin(left *qdata.Table, keys []string, index map[string][]*qdata.Row) *qdata.Table {
	out := &qdata.Table{Columns: slices.Clone(left.GetColumns())}

	for _, lrow := range left.GetRows() {
		if tuple, ok := keyTuple(rowMap(lrow), keys); ok {
			if _, matched := index[tuple]; matched {
				continue
			}
		}

		out.Rows = append(out.Rows, &qdata.Row{Values: cloneAttrs(lrow.GetValues())})
	}

	return out
}

// removeAll returns values with every element of drop removed.
func removeAll(values, drop []string) []string {
	dropSet := toSet(drop)
	out := make([]string, 0, len(values))

	for _, value := range values {
		if _, ok := dropSet[value]; !ok {
			out = append(out, value)
		}
	}

	return out
}

func indexRows(rows []*qdata.Row, keys []string) map[string][]*qdata.Row {
	index := map[string][]*qdata.Row{}

	for _, row := range rows {
		tuple, ok := keyTuple(rowMap(row), keys)
		if !ok {
			continue
		}

		index[tuple] = append(index[tuple], row)
	}

	return index
}

// mergeRow combines a matched left/right row pair: every left column, then every
// right column that is neither a join key (already present from the left) nor a
// name colliding with a left column (renamed with rightPrefix).
func mergeRow(left, right *qdata.Row, leftColumns, keys []string) *qdata.Row {
	out := cloneAttrs(left.GetValues())
	leftSet := toSet(leftColumns)
	keySet := toSet(keys)

	for _, entry := range right.GetValues().GetValues() {
		if _, isKey := keySet[entry.GetKey()]; isKey {
			continue
		}

		name := entry.GetKey()
		if _, clash := leftSet[name]; clash {
			name = rightPrefix + name
		}

		qdata.AttrPut(out, name, entry.GetValue())
	}

	return &qdata.Row{Values: out}
}

// mergedColumns computes the joined schema with the same key-skip and collision-
// rename rules as mergeRow, so columns and rows stay aligned.
func mergedColumns(left, right *qdata.Table, keys []string) []string {
	leftSet := toSet(left.GetColumns())
	keySet := toSet(keys)
	out := slices.Clone(left.GetColumns())

	for _, column := range right.GetColumns() {
		if _, isKey := keySet[column]; isKey {
			continue
		}

		if _, clash := leftSet[column]; clash {
			column = rightPrefix + column
		}

		out = append(out, column)
	}

	return out
}

// sharedColumns is the sorted intersection of two tables' columns.
func sharedColumns(left, right *qdata.Table) []string {
	rightSet := toSet(right.GetColumns())
	shared := make([]string, 0)

	for _, column := range left.GetColumns() {
		if _, ok := rightSet[column]; ok {
			shared = append(shared, column)
		}
	}

	slices.Sort(shared)

	return shared
}

// keyTuple builds the equijoin tuple from a row's key-column values. It reports
// false when the row lacks a key column or a value that cannot be stringified,
// so such a row never matches.
func keyTuple(row map[string]*qdata.Value, keys []string) (string, bool) {
	parts := make([]string, 0, len(keys))

	for _, key := range keys {
		value, ok := row[key]
		if !ok {
			return "", false
		}

		text, ok := valueString(value)
		if !ok {
			return "", false
		}

		parts = append(parts, text)
	}

	return strings.Join(parts, keyDelim), true
}

func rowMap(row *qdata.Row) map[string]*qdata.Value {
	out := make(map[string]*qdata.Value, len(row.GetValues().GetValues()))
	for _, kv := range row.GetValues().GetValues() {
		out[kv.GetKey()] = kv.GetValue()
	}

	return out
}

// valueString renders a scalar Value to a comparable string for join keys.
// Non-scalar values are not joinable and report false.
func valueString(value *qdata.Value) (string, bool) {
	switch inner := value.GetValue().(type) {
	case *qdatav1.Value_StringValue:
		return inner.StringValue, true
	case *qdatav1.Value_DoubleValue:
		return strconv.FormatFloat(inner.DoubleValue, 'f', floatFmt, floatBit), true
	case *qdatav1.Value_IntValue:
		return strconv.FormatInt(inner.IntValue, decimalBase), true
	case *qdatav1.Value_UintValue:
		return strconv.FormatUint(inner.UintValue, decimalBase), true
	case *qdatav1.Value_BoolValue:
		return strconv.FormatBool(inner.BoolValue), true
	default:
		return "", false
	}
}

func toSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}

	return out
}
