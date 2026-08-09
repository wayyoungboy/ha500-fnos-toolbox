// Command ui-preview serves the embedded interface with synthetic, non-private data.
// It is a development tool and is not included in the fnOS package.
package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/config"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/control"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/server"
)

type previewBackend struct {
	mu       sync.Mutex
	settings config.Settings
	led      server.LEDControl
}

func newPreviewBackend() *previewBackend {
	return &previewBackend{
		settings: config.Settings{
			BayPortMap: [5]int{1, 2, 3, 4, 5},
			FanPolicies: control.PolicySet{
				2: {Mode: control.ModeFixed, TemperatureSource: "cpu", MinDutyPercent: 35, FixedDutyPercent: 100, CriticalTemperatureC: 80},
				3: {Mode: control.ModeCurve, TemperatureSource: "cpu", MinDutyPercent: 35, CriticalTemperatureC: 80, Curve: control.Curve{{TemperatureC: 40, DutyPercent: 35}, {TemperatureC: 60, DutyPercent: 55}, {TemperatureC: 75, DutyPercent: 75}, {TemperatureC: 80, DutyPercent: 100}}},
			},
		},
		led: server.LEDControl{Mode: control.LEDAutomatic},
	}
}

func (backend *previewBackend) Snapshot(context.Context) (server.Status, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	cpuTemperature := 42.0
	diskTemperature := 39.0
	return server.Status{
		Version: "0.1.1", Running: true, Hardware: server.HardwareStatus{Supported: true, Match: "ha500-reference-fingerprint", ASM1166: true, GPIO: true, FanChip: "it8620", FanDriver: true},
		Disks:       []server.DiskStatus{{Bay: 1, ControllerPort: 1, Present: true, Device: "sdb", Model: "Disk A"}, {Bay: 2, ControllerPort: 2}, {Bay: 3, ControllerPort: 3, Present: true, Device: "sda", Model: "Disk B"}, {Bay: 4, ControllerPort: 4, Present: true, Device: "sdc", Model: "Disk C"}, {Bay: 5, ControllerPort: 5}},
		Fans:        []server.FanStatus{{Number: 2, Name: "机箱风扇", RPM: 3550, PWM: 255, DutyPercent: 100, Verified: true, Policy: backend.settings.FanPolicies[2]}, {Number: 3, Name: "系统风扇", RPM: 1790, PWM: 143, DutyPercent: 56, Verified: true, Policy: backend.settings.FanPolicies[3]}},
		Temperature: 42, LED: backend.led, UpdatedAt: time.Now().Format(time.RFC3339),
		TemperatureSources: []server.TemperatureSourceStatus{
			{ID: "cpu", Kind: "cpu", Label: "CPU Package", TemperatureC: &cpuTemperature, Available: true},
			{ID: "disk:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: "disk", Label: "DISK 1 · Disk A · ID A1B2C3", TemperatureC: &diskTemperature, Available: true},
		},
	}, nil
}

func (backend *previewBackend) Settings() config.Settings {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.settings
}
func (backend *previewBackend) LEDControl() server.LEDControl {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.led
}
func (backend *previewBackend) SetLEDControl(_ context.Context, value server.LEDControl) error {
	backend.mu.Lock()
	backend.led = value
	backend.mu.Unlock()
	return nil
}
func (backend *previewBackend) SetBayPortMap(_ context.Context, value [5]int) error {
	backend.mu.Lock()
	backend.settings.BayPortMap = value
	backend.mu.Unlock()
	return nil
}
func (backend *previewBackend) SetFanPolicy(_ context.Context, channel int, value control.FanPolicy) error {
	backend.mu.Lock()
	backend.settings.FanPolicies[channel] = value
	backend.mu.Unlock()
	return nil
}
func (*previewBackend) VerifyFan(context.Context, int, bool) (control.Verification, error) {
	return control.Verification{Supported: true, InitialRPM: 1790, ObservedRPM: 2200}, nil
}
func (*previewBackend) ReadLogs(limit int) (server.LogSnapshot, error) {
	return server.LogSnapshot{Limit: limit, MaxBytes: 1 << 20, BackupCount: 1, Items: []server.LogItem{{Timestamp: time.Now().Format(time.RFC3339), Level: "INFO", Message: "preview service started"}}}, nil
}
func (*previewBackend) ClearLogs() error { return nil }

func main() {
	handler := server.NewHandler("/app/ha500_toolbox", newPreviewBackend())
	adminPreview := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Header.Set("X-Trim-Isadmin", "true")
		handler.ServeHTTP(writer, request)
	})
	fmt.Println("UI preview: http://127.0.0.1:18787/app/ha500_toolbox/")
	if err := http.ListenAndServe("127.0.0.1:18787", adminPreview); err != nil {
		panic(err)
	}
}
