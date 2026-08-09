package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/control"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/hardware"
)

type fakeDiskTemperatureProvider struct {
	items []hardware.DiskTemperature
}

func (provider fakeDiskTemperatureProvider) Snapshot(context.Context, map[int]hardware.Disk) ([]hardware.DiskTemperature, error) {
	return provider.items, nil
}

func TestServiceStartsFailClosedWithSafeDefaults(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{
		SysRoot:       root,
		CPUInfoPath:   filepath.Join(root, "proc/cpuinfo"),
		SettingsPath:  filepath.Join(root, "var/settings.json"),
		LogPath:       filepath.Join(root, "var/toolbox.log"),
		Version:       "0.1.0",
		ConflictCheck: func() string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	settings := service.Settings()
	if settings.BayPortMap != [5]int{1, 2, 3, 4, 5} || len(settings.FanPolicies) != 0 {
		t.Fatalf("unsafe defaults: %+v", settings)
	}
	status, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Hardware.Supported || !status.MonitorOnly || status.Version != "0.1.0" {
		t.Fatalf("status = %+v; want unsupported monitor-only service", status)
	}
}

func TestStatusPublishesDiskTemperatureSourcesWithoutPlainSerial(t *testing.T) {
	root := t.TempDir()
	source := hardware.DiskTemperatureSourceID("PRIVATE-SERIAL-1234")
	service, err := New(Options{
		SysRoot:      root,
		CPUInfoPath:  filepath.Join(root, "proc/cpuinfo"),
		SettingsPath: filepath.Join(root, "var/settings.json"),
		LogPath:      filepath.Join(root, "var/toolbox.log"),
		Version:      "0.2.0",
		DiskTemperatures: fakeDiskTemperatureProvider{items: []hardware.DiskTemperature{
			{SourceID: source, DisplayID: "A1B2C3", Device: "sda", Model: "Example Disk", ControllerPort: 1, TemperatureC: 40, Available: true},
		}},
		ConflictCheck: func() string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	status, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.TemperatureSources) != 2 || status.TemperatureSources[1].ID != source || status.TemperatureSources[1].TemperatureC == nil || *status.TemperatureSources[1].TemperatureC != 40 {
		t.Fatalf("temperature sources = %+v", status.TemperatureSources)
	}
	if status.TemperatureSources[1].Label == "" {
		t.Fatal("disk temperature source has no human-readable label")
	}
}

func TestConflictPreventsVerificationAndControlledPolicy(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{
		SysRoot:       root,
		CPUInfoPath:   filepath.Join(root, "proc/cpuinfo"),
		SettingsPath:  filepath.Join(root, "var/settings.json"),
		LogPath:       filepath.Join(root, "var/toolbox.log"),
		Version:       "0.1.0",
		ConflictCheck: func() string { return "coolercontrold" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.VerifyFan(context.Background(), 3, false); !errors.Is(err, ErrControlConflict) {
		t.Fatalf("VerifyFan error = %v; want ErrControlConflict", err)
	}
	policy := control.FanPolicy{Mode: control.ModeFixed, TemperatureSource: "cpu", MinDutyPercent: 35, FixedDutyPercent: 56, CriticalTemperatureC: 80}
	if err := service.SetFanPolicy(context.Background(), 3, policy); !errors.Is(err, ErrControlConflict) {
		t.Fatalf("SetFanPolicy error = %v; want ErrControlConflict", err)
	}
	if err := service.SetFanPolicy(context.Background(), 3, control.FanPolicy{Mode: control.ModeMonitor}); err != nil {
		t.Fatalf("monitor policy should remain available during a conflict: %v", err)
	}
}

func TestUnsupportedHardwareCannotVerifyOrTakeFanControl(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{
		SysRoot:       root,
		CPUInfoPath:   filepath.Join(root, "proc/cpuinfo"),
		SettingsPath:  filepath.Join(root, "var/settings.json"),
		LogPath:       filepath.Join(root, "var/toolbox.log"),
		Version:       "0.1.0",
		ConflictCheck: func() string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.VerifyFan(context.Background(), 3, false); !errors.Is(err, ErrUnsupportedHardware) {
		t.Fatalf("VerifyFan error = %v; want ErrUnsupportedHardware", err)
	}
	policy := control.FanPolicy{Mode: control.ModeFixed, TemperatureSource: "cpu", MinDutyPercent: 35, FixedDutyPercent: 56, CriticalTemperatureC: 80}
	if err := service.SetFanPolicy(context.Background(), 3, policy); !errors.Is(err, ErrUnsupportedHardware) {
		t.Fatalf("SetFanPolicy error = %v; want ErrUnsupportedHardware", err)
	}
}
