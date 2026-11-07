package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// FlexInt accepts integer JSON values and numeric strings used by some peers.
type FlexInt int

// UnmarshalJSON decodes a JSON integer or a quoted decimal integer.
func (i *FlexInt) UnmarshalJSON(data []byte) error {
	value := bytes.TrimSpace(data)
	if len(value) == 0 {
		return fmt.Errorf("protocol: cannot unmarshal empty value into FlexInt")
	}
	if value[0] == '"' {
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return err
		}
		parsed, err := strconv.Atoi(text)
		if err != nil {
			return fmt.Errorf("protocol: cannot unmarshal %q into FlexInt: %w", text, err)
		}
		*i = FlexInt(parsed)
		return nil
	}
	parsed, err := strconv.Atoi(string(value))
	if err != nil {
		return fmt.Errorf("protocol: cannot unmarshal %s into FlexInt: %w", value, err)
	}
	*i = FlexInt(parsed)
	return nil
}
