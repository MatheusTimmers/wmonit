package gitlab

import "testing"

func TestTitleParsing(t *testing.T) {
	cases := []struct {
		title, key, kind, short string
	}{
		{"add new login flow #GAR-123 [feature]", "GAR-123", "feature", "add new login flow"},
		{"fix timeout on orders #gar-7 [Enhancement]", "GAR-7", "enhancement", "fix timeout on orders"},
		{"refactor session handling", "", "", "refactor session handling"},
		{"#ABC-99 [bugfix] handle nil pointer", "ABC-99", "bugfix", "handle nil pointer"},
	}
	for _, c := range cases {
		mr := MR{Title: c.title}
		if got := mr.JiraKey(); got != c.key {
			t.Errorf("JiraKey(%q) = %q, esperado %q", c.title, got, c.key)
		}
		if got := mr.Kind(); got != c.kind {
			t.Errorf("Kind(%q) = %q, esperado %q", c.title, got, c.kind)
		}
		if got := mr.ShortTitle(); got != c.short {
			t.Errorf("ShortTitle(%q) = %q, esperado %q", c.title, got, c.short)
		}
	}
}

func TestSummaryMine(t *testing.T) {
	s := &Summary{
		OpenMRs: []MR{{ProjectID: 1, IID: 10}, {ProjectID: 1, IID: 11}},
		Merged:  []MR{{ProjectID: 1, IID: 10}, {ProjectID: 2, IID: 10}},
		Closed:  []MR{{ProjectID: 1, IID: 12}, {ProjectID: 1, IID: 11}},
	}
	mine := s.Mine()
	if len(mine) != 4 {
		t.Fatalf("Mine() = %d MRs, esperado 4 (dedup de abertos+mergeados+fechados)", len(mine))
	}
}
