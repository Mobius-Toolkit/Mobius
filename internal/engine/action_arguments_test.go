package engine

import (
	"slices"
	"testing"
)

func TestActionArgumentsFollowTheOrderOfTheTool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tool, arguments string
		want            []string
	}{
		{"edit_brief", `{"brief":"B.","reason":"R."}`, []string{"reason", "brief"}},
		{"create_workstream", `{"brief":"B.","labels":["a"],"title":"T"}`, []string{"title", "labels", "brief"}},
		{"create_workstream", `{"extra":1,"brief":"B.","title":"T"}`, []string{"title", "brief", "extra"}},
		{"move_task", `{"workstream":3,"n":7,"repository":"o/r"}`, []string{"repository", "n", "workstream"}},
		{"other", `{"b":1,"a":2}`, []string{"b", "a"}},
	}
	for _, test := range tests {
		found, err := ActionArguments(test.tool, test.arguments)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, argument := range found {
			names = append(names, argument.Name)
		}
		if !slices.Equal(names, test.want) {
			t.Errorf("%s: names = %v, want %v", test.tool, names, test.want)
		}
	}
}
