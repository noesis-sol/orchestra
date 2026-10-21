package main

import (
	"testing"
	"testing/fstest"
)

// TestSysfsOnBatteryDeviceScope: a wireless mouse's battery, scope Device and discharging, on a desktop with no
// mains supply listed leaves the machine on mains; the machine's own battery, scope System, still counts.
func TestSysfsOnBatteryDeviceScope(t *testing.T) {
	t.Parallel()
	file := func(text string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(text + "\n")} }
	mouse := fstest.MapFS{
		"hidpp_battery_0/type":   file("Battery"),
		"hidpp_battery_0/scope":  file("Device"),
		"hidpp_battery_0/status": file("Discharging"),
	}
	if battery, err := sysfsOnBattery(mouse); err != nil || battery {
		t.Errorf("a desktop with a wireless mouse: %v, %v", battery, err)
	}
	laptop := fstest.MapFS{
		"hidpp_battery_0/type":   file("Battery"),
		"hidpp_battery_0/scope":  file("Device"),
		"hidpp_battery_0/status": file("Discharging"),
		"BAT0/type":              file("Battery"),
		"BAT0/scope":             file("System"),
		"BAT0/status":            file("Discharging"),
	}
	if battery, err := sysfsOnBattery(laptop); err != nil || !battery {
		t.Errorf("a laptop on its battery, with a wireless mouse: %v, %v", battery, err)
	}
}
