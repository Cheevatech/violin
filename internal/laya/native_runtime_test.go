//go:build cgo

package laya

import "testing"

func TestQuestionTypeIDsMatchUpstreamLaya(t *testing.T) {
	for _, tc := range []struct {
		kind Kind
		want int
	}{{Choice, 0}, {Score, 1}, {Noul, 2}} {
		if got := questionTypeID(tc.kind); got != tc.want {
			t.Errorf("questionTypeID(%q) = %d, want %d", tc.kind, got, tc.want)
		}
	}
	if got := questionTypeID("unsupported"); got != -1 {
		t.Fatalf("unsupported question type id = %d, want -1", got)
	}
}
