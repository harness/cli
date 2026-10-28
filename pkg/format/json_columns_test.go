package format

import (
	"testing"

	"github.com/harness/cli/v3/pkg/spec"
)

func TestProjectColumns_KeysByIDAndNullOnFailedExpr(t *testing.T) {
	tspec := &spec.TableSpec{Columns: []spec.TableColumn{
		{ID: "id", Expr: "it.planExecutionId"},
		{ID: "bad", Expr: "it.missing.deeper"},
	}}
	rows := []any{map[string]any{"planExecutionId": "abc"}}

	got := projectColumns(tspec, rows, map[string]any{})

	obj := got[0].(map[string]any)
	if obj["id"] != "abc" {
		t.Fatalf("id = %v, want abc", obj["id"])
	}
	if v, ok := obj["bad"]; !ok || v != nil {
		t.Fatalf("bad = %v (present=%v), want nil present", v, ok)
	}
}
