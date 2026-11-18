package qdata_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

func TestTableColumnMatches(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		column string
		label  string
		want   bool
	}{
		{column: "user_email", label: "user_email", want: true},
		{column: "right_user_email", label: "user_email", want: true},
		{column: "label_value", label: "value", want: true},
		{column: "right_label_value", label: "value", want: true},
		{column: "right_right_user_email", label: "right_user_email", want: true},
		{column: "right_user_email", label: "right_user_email", want: true},
		{column: "user_email", label: "right_user_email", want: false},
		{column: "bright_user_email", label: "user_email", want: false},
		{column: "job", label: "user_email", want: false},
	} {
		t.Run(test.column+"/"+test.label, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, qdata.TableColumnMatches(test.column, []string{test.label}))
			assert.False(t, qdata.TableColumnMatches(test.column, nil))
		})
	}
}
