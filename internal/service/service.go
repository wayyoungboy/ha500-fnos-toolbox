package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/config"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/control"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/hardware"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/logstore"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/server"
)

const (
	defaultLogBytes   = 1 << 20
	defaultLogBackups = 1
)

var ErrControlConflict = errors.New("another fan-control service is active")

type Options struct {
	SysRoot       string
	CPUInfoPath   string
	SettingsPath  string
	LogPath       string
	Version       string
	ConflictCheck func() string
	Settle        func(context.Context) error
}

type Service struct {
	mu            sync.RWMutex
	options       Options
	store         *config.Store
	logs          *logstore.Store
	logger        *slog.Logger
	settings      config.Settings
	ledControl    server.LEDControl
	status        server.Status
	fanHardware   hardware.FanHardware
	fanController *control.FanController
	closed        bool
}

func New(options Options) (*Service, error) {
	if options.SysRoot == "" {
		options.SysRoot = "/sys"
	}
	if options.CPUInfoPath == "" {
		options.CPUInfoPath = "/proc/cpuinfo"
	}
	if options.ConflictCheck == nil {
		options.ConflictCheck = DefaultConflictCheck
	}
	if options.Settle == nil {
		options.Settle = func(ctx context.Context) error {
			timer := time.NewTimer(4 * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	logs, err := logstore.New(options.LogPath, defaultLogBytes, defaultLogBackups)
	if err != nil {
		return nil, fmt.Errorf("open service log: %w", err)
	}
	store := config.NewStore(options.SettingsPath)
	settings, err := store.LoadOrDefault()
	if err != nil {
		_ = logs.Close()
		return nil, fmt.Errorf("load settings: %w", err)
	}
	if settings.FanPolicies == nil {
		settings.FanPolicies = make(control.PolicySet)
	}
	if settings.VerifiedFans == nil {
		settings.VerifiedFans = make(map[int]bool)
	}
	service := &Service{
		options:    options,
		store:      store,
		logs:       logs,
		logger:     slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})),
		settings:   settings,
		ledControl: server.LEDControl{Mode: control.LEDAutomatic},
	}
	service.logger.Info("HA500 toolbox service starting", "version", options.Version)
	service.refresh(context.Background())
	return service, nil
}

func (service *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			service.refresh(ctx)
		}
	}
}

func (service *Service) refresh(ctx context.Context) {
	report := hardware.DetectHA500(service.options.SysRoot, service.options.CPUInfoPath)
	disks, diskErr := hardware.DiscoverDisks(service.options.SysRoot)
	fans, fanErr := hardware.DiscoverFans(service.options.SysRoot)
	conflict := service.options.ConflictCheck()

	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return
	}
	for index := range fans.Channels {
		fans.Channels[index].Verified = service.settings.VerifiedFans[fans.Channels[index].Number]
	}
	service.fanHardware = fans
	status := server.Status{
		Version:     service.options.Version,
		Running:     true,
		MonitorOnly: true,
		Hardware: server.HardwareStatus{
			Supported: report.Supported,
			Match:     report.Match,
			ASM1166:   report.ASM1166Count == 1,
			GPIO:      report.GPIOFound,
			FanChip:   fans.ChipName,
			FanDriver: fanErr == nil,
			Conflict:  conflict,
		},
		Temperature: float64(fans.Temperature.MilliCelsius) / 1000,
		LED:         service.ledControl,
		UpdatedAt:   time.Now().Format(time.RFC3339Nano),
	}
	for bay, port := range service.settings.BayPortMap {
		disk, present := disks[port]
		status.Disks = append(status.Disks, server.DiskStatus{Bay: bay + 1, ControllerPort: port, Present: present, Device: disk.Device, Model: disk.Model})
	}
	for _, channel := range fans.Channels {
		policy, exists := service.settings.FanPolicies[channel.Number]
		if !exists {
			policy = control.FanPolicy{Mode: control.ModeMonitor}
		}
		status.Fans = append(status.Fans, server.FanStatus{
			Number: channel.Number, Name: fanName(channel.Number), RPM: channel.RPM, PWM: channel.PWM,
			DutyPercent: (channel.PWM*100 + 127) / 255, Verified: channel.Verified, Policy: policy,
		})
	}
	var lastErrors []string
	if diskErr != nil {
		lastErrors = append(lastErrors, diskErr.Error())
	}
	if fanErr != nil {
		lastErrors = append(lastErrors, fanErr.Error())
	}
	if report.Supported && report.GPIOFound {
		writer, err := hardware.NewSysfsGPIO(service.options.SysRoot, hardware.PCHGPIOLabel)
		if err == nil {
			present := [5]bool{}
			for index := range status.Disks {
				present[index] = status.Disks[index].Present
			}
			err = control.NewLEDController(writer).Apply(service.ledControl.Mode, service.ledControl.States, present)
		}
		if err != nil {
			lastErrors = append(lastErrors, "LED control: "+err.Error())
		}
	}
	if report.Supported && fanErr == nil && conflict == "" {
		sensor := fileSensorIO{}
		temperaturePaths := map[string]string{}
		if fans.Temperature.InputPath != "" {
			temperaturePaths["cpu"] = fans.Temperature.InputPath
		}
		controller := control.NewFanController(sensor, fans.Channels, temperaturePaths)
		service.fanController = controller
		if hasControlledPolicy(service.settings.FanPolicies) {
			if err := controller.Apply(ctx, service.settings.FanPolicies); err != nil {
				lastErrors = append(lastErrors, "fan control: "+err.Error())
				service.logger.Error("fan control failed; owned fans switched to full speed", "error", err)
			} else {
				status.MonitorOnly = false
			}
		}
	}
	status.LastError = strings.Join(lastErrors, "; ")
	service.status = status
}

func (service *Service) Snapshot(ctx context.Context) (server.Status, error) {
	service.refresh(ctx)
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.status, nil
}

func (service *Service) Settings() config.Settings {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return cloneSettings(service.settings)
}

func (service *Service) LEDControl() server.LEDControl {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.ledControl
}

func (service *Service) SetLEDControl(ctx context.Context, value server.LEDControl) error {
	if value.Mode != control.LEDAutomatic && value.Mode != control.LEDManual {
		return errors.New("invalid LED mode")
	}
	service.mu.Lock()
	service.ledControl = value
	service.mu.Unlock()
	service.logger.Info("LED mode changed", "mode", value.Mode)
	service.refresh(ctx)
	return nil
}

func (service *Service) SetBayPortMap(ctx context.Context, mapping [5]int) error {
	if _, err := control.ValidateBayPortMap(mapping[:]); err != nil {
		return err
	}
	service.mu.Lock()
	next := cloneSettings(service.settings)
	next.BayPortMap = mapping
	if err := service.store.Save(next); err != nil {
		service.mu.Unlock()
		return err
	}
	service.settings = next
	service.mu.Unlock()
	service.logger.Info("disk bay mapping updated")
	service.refresh(ctx)
	return nil
}

func (service *Service) SetFanPolicy(ctx context.Context, channel int, policy control.FanPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	conflict := service.options.ConflictCheck()
	if policy.Mode != control.ModeMonitor && conflict != "" {
		return fmt.Errorf("%w: %s", ErrControlConflict, conflict)
	}
	service.mu.Lock()
	if policy.Mode != control.ModeMonitor {
		known := false
		for _, fan := range service.fanHardware.Channels {
			if fan.Number == channel {
				known = true
				break
			}
		}
		if !known {
			service.mu.Unlock()
			return fmt.Errorf("fan channel %d is unavailable", channel)
		}
		if !service.settings.VerifiedFans[channel] {
			service.mu.Unlock()
			return fmt.Errorf("fan channel %d has not passed software-control verification", channel)
		}
	}
	next := cloneSettings(service.settings)
	next.FanPolicies[channel] = policy
	if err := service.store.Save(next); err != nil {
		service.mu.Unlock()
		return err
	}
	service.settings = next
	service.mu.Unlock()
	service.logger.Info("fan policy updated", "channel", channel, "mode", policy.Mode)
	service.refresh(ctx)
	return nil
}

func (service *Service) VerifyFan(ctx context.Context, channel int, allowLowering bool) (control.Verification, error) {
	if conflict := service.options.ConflictCheck(); conflict != "" {
		return control.Verification{}, fmt.Errorf("%w: %s", ErrControlConflict, conflict)
	}
	service.mu.RLock()
	var selected hardware.FanChannel
	for _, fan := range service.fanHardware.Channels {
		if fan.Number == channel {
			selected = fan
			break
		}
	}
	service.mu.RUnlock()
	if selected.Number == 0 {
		return control.Verification{}, fmt.Errorf("fan channel %d is unavailable", channel)
	}
	result, err := control.VerifySoftwareControl(ctx, fileSensorIO{}, selected, control.VerifyOptions{AllowLoweringAtFull: allowLowering, Settle: service.options.Settle})
	if err != nil {
		return result, err
	}
	if !result.Supported {
		return result, errors.New("PWM changed but RPM did not respond; check BIOS/EC and PWM/DC mode")
	}
	service.mu.Lock()
	next := cloneSettings(service.settings)
	next.VerifiedFans[channel] = true
	if err := service.store.Save(next); err != nil {
		service.mu.Unlock()
		return result, err
	}
	service.settings = next
	service.mu.Unlock()
	service.logger.Info("fan software control verified", "channel", channel, "initial_rpm", result.InitialRPM, "observed_rpm", result.ObservedRPM)
	service.refresh(ctx)
	return result, nil
}

func (service *Service) ReadLogs(limit int) (server.LogSnapshot, error) {
	snapshot, err := service.logs.Read(limit)
	if err != nil {
		return server.LogSnapshot{}, err
	}
	result := server.LogSnapshot{Limit: snapshot.Limit, MaxBytes: snapshot.MaxBytes, BackupCount: snapshot.BackupCount}
	for _, item := range snapshot.Items {
		result.Items = append(result.Items, server.LogItem{Timestamp: item.Timestamp, Level: item.Level, Message: item.Message})
	}
	for _, file := range snapshot.Files {
		result.Files = append(result.Files, server.LogFile{Name: file.Name, Size: file.Size})
	}
	return result, nil
}

func (service *Service) ClearLogs() error {
	if err := service.logs.Clear(); err != nil {
		return err
	}
	service.logger.Info("service log cleared")
	return nil
}

func (service *Service) Close() error {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return nil
	}
	service.closed = true
	controller := service.fanController
	policies := service.settings.FanPolicies
	service.mu.Unlock()
	var failSafeErr error
	if controller != nil {
		failSafeErr = controller.FailSafe(policies)
	}
	service.logger.Info("HA500 toolbox service stopped; owned fans set to full speed")
	return errors.Join(failSafeErr, service.logs.Close())
}

func DefaultConflictCheck() string {
	for _, unit := range []string{"coolercontrold.service", "fancontrol.service"} {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		output, err := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
		cancel()
		if err == nil && strings.TrimSpace(string(output)) == "active" {
			return strings.TrimSuffix(unit, ".service")
		}
	}
	return ""
}

type fileSensorIO struct{}

func (fileSensorIO) ReadInt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func (fileSensorIO) WriteInt(path string, value int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(value)+"\n"), 0o600)
}

func cloneSettings(source config.Settings) config.Settings {
	result := source
	result.FanPolicies = make(control.PolicySet, len(source.FanPolicies))
	for channel, policy := range source.FanPolicies {
		policy.Curve = append(control.Curve(nil), policy.Curve...)
		result.FanPolicies[channel] = policy
	}
	result.VerifiedFans = make(map[int]bool, len(source.VerifiedFans))
	for channel, verified := range source.VerifiedFans {
		result.VerifiedFans[channel] = verified
	}
	return result
}

func fanName(channel int) string {
	switch channel {
	case 2:
		return "机箱风扇"
	case 3:
		return "系统风扇"
	default:
		return fmt.Sprintf("风扇 %d", channel)
	}
}

func hasControlledPolicy(policies control.PolicySet) bool {
	channels := make([]int, 0, len(policies))
	for channel := range policies {
		channels = append(channels, channel)
	}
	sort.Ints(channels)
	for _, channel := range channels {
		if policies[channel].Mode != control.ModeMonitor {
			return true
		}
	}
	return false
}
