package hardware

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSmartctlReaderUsesStandbySafeJSONAndAcceptsHealthExitBits(t *testing.T) {
	script := filepath.Join(t.TempDir(), "smartctl")
	body := `#!/bin/sh
if [ "$*" != "-n standby,0 -j -a /dev/sda" ]; then
  exit 98
fi
printf '%s\n' '{"serial_number":"PRIVATE-SERIAL-1234","model_name":"Example Disk","temperature":{"current":41}}'
exit 64
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	identity, err := NewSmartctlReader(script).Read(context.Background(), "sda")
	if err != nil {
		t.Fatalf("Read returned error for valid SMART JSON: %v", err)
	}
	if identity.Serial != "PRIVATE-SERIAL-1234" || identity.Model != "Example Disk" || identity.TemperatureC == nil || *identity.TemperatureC != 41 {
		t.Fatalf("identity = %+v", identity)
	}
	if _, err := NewSmartctlReader(script).Read(context.Background(), "../../etc/passwd"); err == nil {
		t.Fatal("unsafe block device name was accepted")
	}
}

type fakeSMARTReader struct {
	mu      sync.Mutex
	results map[string]SMARTIdentity
	calls   []string
}

func (reader *fakeSMARTReader) Read(_ context.Context, device string) (SMARTIdentity, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.calls = append(reader.calls, device)
	return reader.results[device], nil
}

func TestSMARTMonitorKeysSourcesBySerialFingerprintAndCachesReads(t *testing.T) {
	temperature := 39.5
	reader := &fakeSMARTReader{results: map[string]SMARTIdentity{
		"sda": {Serial: "PRIVATE-SERIAL-1234", Model: "Example Disk", TemperatureC: &temperature},
	}}
	monitor := NewSMARTMonitor(reader, time.Hour)
	disks := map[int]Disk{2: {Device: "sda", Model: "Example Disk", ControllerPort: 2}}

	first, err := monitor.Snapshot(context.Background(), disks)
	if err != nil {
		t.Fatal(err)
	}
	second, err := monitor.Snapshot(context.Background(), disks)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 || len(reader.calls) != 1 {
		t.Fatalf("first=%+v second=%+v calls=%v", first, second, reader.calls)
	}
	wantSource := DiskTemperatureSourceID("PRIVATE-SERIAL-1234")
	if first[0].SourceID != wantSource || first[0].DisplayID == "" || first[0].TemperatureC != 39.5 || !first[0].Available {
		t.Fatalf("disk temperature = %+v", first[0])
	}
	if strings.Contains(first[0].DisplayID, "1234") {
		t.Fatalf("display ID leaked serial material: %q", first[0].DisplayID)
	}
}

func TestSMARTMonitorKeepsIdentityWhenDeviceNameChanges(t *testing.T) {
	temperature := 37.0
	reader := &fakeSMARTReader{results: map[string]SMARTIdentity{
		"sda": {Serial: "STABLE-SERIAL", Model: "Disk", TemperatureC: &temperature},
		"sdc": {Serial: "STABLE-SERIAL", Model: "Disk", TemperatureC: &temperature},
	}}
	monitor := NewSMARTMonitor(reader, 0)

	before, err := monitor.Snapshot(context.Background(), map[int]Disk{1: {Device: "sda", ControllerPort: 1}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := monitor.Snapshot(context.Background(), map[int]Disk{4: {Device: "sdc", ControllerPort: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || len(after) != 1 || before[0].SourceID != after[0].SourceID {
		t.Fatalf("source changed with device name: before=%+v after=%+v", before, after)
	}
	if after[0].Device != "sdc" || after[0].ControllerPort != 4 {
		t.Fatalf("current topology was not refreshed: %+v", after[0])
	}
}
