package mousepad

import "testing"

func TestMapSpecialKeyHasEverySupportedValue(t *testing.T) {
	for key := 1; key <= 32; key++ {
		if got := mapSpecialKey(key); got == "" {
			t.Errorf("special key %d is unmapped", key)
		}
		if got := mapUinputKey(key); got == -1 {
			t.Errorf("uinput special key %d is unmapped", key)
		}
	}
	if got := mapSpecialKey(0); got != "" {
		t.Errorf("special key 0 should be unmapped, got %q", got)
	}
	if got := mapSpecialKey(33); got != "" {
		t.Errorf("special key 33 should be unmapped, got %q", got)
	}
	if got := mapUinputKey(0); got != -1 {
		t.Errorf("uinput special key 0 should be -1, got %d", got)
	}
	if got := mapUinputKey(33); got != -1 {
		t.Errorf("uinput special key 33 should be -1, got %d", got)
	}
}
