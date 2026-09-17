package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FlexBool is a bool that tolerates the loose encodings stock clients
// emit: JSON booleans, 0/1 numbers, and "true"/"false"/"1"/"0" strings
// (Android sends SMS read flags as 0/1 and telephony cancel flags as
// the string "true"). It marshals back as a plain JSON boolean.
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
