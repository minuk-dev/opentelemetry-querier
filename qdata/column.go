package qdata

import (
	"slices"
	"strings"
)

const (
	// RightColumnPrefix disambiguates a non-key column shared by both join sides.
	RightColumnPrefix = "right_"
	// ShadowedLabelPrefix preserves labels displaced by synthetic columns.
	ShadowedLabelPrefix = "label_"
)

// TableColumnMatches checks both the literal column and its names before join
// renaming. Literal prefixed labels are conservatively treated as aliases too.
func TableColumnMatches(column string, labels []string) bool {
	for {
		if slices.Contains(labels, column) {
			return true
		}

		switch {
		case strings.HasPrefix(column, RightColumnPrefix):
			column = strings.TrimPrefix(column, RightColumnPrefix)
		case strings.HasPrefix(column, ShadowedLabelPrefix):
			column = strings.TrimPrefix(column, ShadowedLabelPrefix)
		default:
			return false
		}
	}
}
