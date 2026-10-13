package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FlexBool tolerates the loose encodings stock clients emit (bools, 0/1,
// "true"/"false" strings); it marshals back as a plain JSON boolean.
type FlexBool bool

// UnmarshalJSON accepts booleans, numbers, and strings.
func (b *FlexBool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch t := v.(type) {
	case bool:
		*b = FlexBool(t)
	case float64:
		*b = t != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		*b = s == "true" || s == "1"
	default:
		return fmt.Errorf("protocol: cannot unmarshal %T into FlexBool", v)
	}
	return nil
}
