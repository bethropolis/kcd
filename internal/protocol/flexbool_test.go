package protocol

import (
	"encoding/json"
	"testing"
)

func TestFlexBoolUnmarshal(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`true`, true},
		{`false`, false},
		{`1`, true},
		{`0`, false},
		{`"true"`, true},
		{`"false"`, false},
		{`"1"`, true},
		{`"0"`, false},
	}
	for _, tc := range cases {
		var b FlexBool
		if err := json.Unmarshal([]byte(tc.in), &b); err != nil {
			t.Errorf("unmarshal %s: %v", tc.in, err)
			continue
		}
		if bool(b) != tc.want {
			t.Errorf("unmarshal %s = %v, want %v", tc.in, b, tc.want)
		}
	}
}
