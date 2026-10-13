package battery

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// powerSupplyRoots lists paths to check for battery sysfs entries.
var powerSupplyRoots = []string{
	"/sys/class/power_supply",
	"/sys/devices/platform/subsystem/power_supply",
}

// readLocalBattery reads local sysfs state (charge 0-100, charging, error);
// no battery is an error — callers log and skip.
func readLocalBattery() (int, bool, error) {
	for _, root := range powerSupplyRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "BAT") {
				continue
			}
			base := filepath.Join(root, name)

			capRaw, err := os.ReadFile(filepath.Join(base, "capacity"))
			if err != nil {
				continue
			}
			capacity, err := strconv.Atoi(strings.TrimSpace(string(capRaw)))
			if err != nil {
				continue
			}

			statusRaw, _ := os.ReadFile(filepath.Join(base, "status"))
			status := strings.TrimSpace(string(statusRaw))
			charging := status == "Charging"

			return capacity, charging, nil
		}
	}

	return 0, false, errNoBattery
}

// errNoBattery is returned when no battery sysfs entry is found.
var errNoBattery = &noBatteryError{}

type noBatteryError struct{}

func (e *noBatteryError) Error() string { return "no battery found" }
func (e *noBatteryError) Is(target error) bool {
	_, ok := target.(*noBatteryError)
	return ok
}
