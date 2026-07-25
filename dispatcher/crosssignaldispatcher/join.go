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
// + body); a Table result passes through. Other payloads fail closed.
func resultToTable(signal qdata.Signal, result *qdata.Result) (*qdata.Table, error) {
	switch data := result.GetData().(type) {
	case *qdatav1.Result_Metrics:
		return rowsToTable(metricRows(data.Metrics)), nil
	case *qdatav1.Result_Logs:
		return rowsToTable(logRows(data.Logs)), nil
	case *qdatav1.Result_Table:
		return data.Table, nil
	default:
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignaldispatcher: cannot join signal %s result (unsupported payload)", signal)
	}
}

func metricRows(metrics *qdata.Metrics) []*qdata.Row {
	rows := make([]*qdata.Row, 0, len(metrics.GetSeries()))

	for _, series := range metrics.GetSeries() {
		kvl := cloneAttrs(series.GetAttributes())

		if name := series.GetName(); name != "" {
			qdata.AttrPut(kvl, metricNameColumn, qdata.Str(name))
		}

		qdata.AttrPut(kvl, metricValueColumn, latestValue(series))
		rows = append(rows, &qdata.Row{Values: kvl})
	}

	return rows
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
// the series has no points.
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
	columns := make([]string, 0)
	seen := map[string]struct{}{}

	for _, row := range rows {
		for _, kv := range row.GetValues().GetValues() {
			if _, ok := seen[kv.GetKey()]; !ok {
				seen[kv.GetKey()] = struct{}{}
				columns = append(columns, kv.GetKey())
			}
		}
	}

	return &qdata.Table{Columns: columns, Rows: rows}
}

// joinTables inner-joins two normalized tables on the given keys. When no keys
// are given it joins on the tables' shared columns; a join with no usable key
// fails closed (a cross product of metrics × logs is meaningless).
func joinTables(left, right *qdata.Table, onKeys []string) (*qdata.Table, error) {
	keys := onKeys
	if len(keys) == 0 {
		keys = sharedColumns(left, right)
	}

	if len(keys) == 0 {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignaldispatcher: no join key (sides share no columns and no `on` was given)")
	}

	index := indexRows(right.GetRows(), keys)
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

	return joined, nil
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
