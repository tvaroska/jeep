package main

import (
	"reflect"
	"testing"
)

func TestParseChapters(t *testing.T) {
	tests := []struct {
		spec string
		max  int
		want []int
		err  bool
	}{
		{"1", 10, []int{1}, false},
		{"1,3,5", 10, []int{1, 3, 5}, false},
		{"1-3", 10, []int{1, 2, 3}, false},
		{"1,3-5,7", 10, []int{1, 3, 4, 5, 7}, false},
		{"5,3,1", 10, []int{1, 3, 5}, false}, // sorted
		{"1,1,2", 10, []int{1, 2}, false},    // deduped
		{"1-5", 3, nil, true},                // range exceeds max
		{"0", 10, nil, true},                 // out of bounds
		{"11", 10, nil, true},                // out of bounds
		{"bad", 10, nil, true},               // invalid number
		{"1-", 10, nil, true},                // invalid range
	}

	for _, tt := range tests {
		got, err := parseChapters(tt.spec, tt.max)
		if tt.err {
			if err == nil {
				t.Errorf("parseChapters(%q, %d) expected error, got %v", tt.spec, tt.max, got)
			}
		} else {
			if err != nil {
				t.Errorf("parseChapters(%q, %d) unexpected error: %v", tt.spec, tt.max, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseChapters(%q, %d) = %v, want %v", tt.spec, tt.max, got, tt.want)
			}
		}
	}
}
