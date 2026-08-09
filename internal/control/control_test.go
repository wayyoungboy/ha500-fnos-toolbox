package control

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/hardware"
)

func TestValidateBayPortMapRequiresFiveUniquePorts(t *testing.T) {
	valid, err := ValidateBayPortMap([]int{6, 4, 3, 2, 1})
	if err != nil || valid != [5]int{6, 4, 3, 2, 1} {
		t.Fatalf("ValidateBayPortMap returned %v, %v", valid, err)
	}
	for _, invalid := range [][]int{{1, 2, 3, 4}, {1, 2, 3, 4, 4}, {0, 2, 3, 4, 5}, {1, 2, 3, 4, 7}} {
		if _, err := ValidateBayPortMap(invalid); err == nil {
			t.Fatalf("ValidateBayPortMap(%v) returned nil error", invalid)
		}
	}
}

type fakeGPIO struct {
	writes [][2]int
}

func (fake *fakeGPIO) Write(offset, value int) error {
	fake.writes = append(fake.writes, [2]int{offset, value})
	return nil
}

func TestLEDControllerAppliesActiveLowAutomaticAndManualStates(t *testing.T) {
	gpio := &fakeGPIO{}
	controller := NewLEDController(gpio)
	if err := controller.Apply(LEDAutomatic, [5]bool{}, [5]bool{true, false, true, false, false}); err != nil {
		t.Fatal(err)
	}
	wantAuto := [][2]int{{75, 0}, {261, 1}, {100, 0}, {260, 1}, {256, 1}}
	if !reflect.DeepEqual(gpio.writes, wantAuto) {
		t.Fatalf("automatic writes = %v; want %v", gpio.writes, wantAuto)
	}
	gpio.writes = nil
	if err := controller.Apply(LEDManual, [5]bool{false, true, false, true, true}, [5]bool{}); err != nil {
		t.Fatal(err)
	}
	wantManual := [][2]int{{75, 1}, {261, 0}, {100, 1}, {260, 0}, {256, 0}}
	if !reflect.DeepEqual(gpio.writes, wantManual) {
		t.Fatalf("manual writes = %v; want %v", gpio.writes, wantManual)
	}
}

func TestCurveDutyInterpolatesAndClamps(t *testing.T) {
	curve := Curve{{TemperatureC: 40, DutyPercent: 35}, {TemperatureC: 60, DutyPercent: 55}, {TemperatureC: 80, DutyPercent: 100}}
	for _, test := range []struct {
		temp float64
		want int
	}{{20, 35}, {40, 35}, {50, 45}, {60, 55}, {90, 100}} {
		if got := curve.Duty(test.temp); got != test.want {
			t.Fatalf("Duty(%v) = %d; want %d", test.temp, got, test.want)
		}
	}
}

func TestIndependentPoliciesCalculateDifferentDuties(t *testing.T) {
	policies := PolicySet{
		2: {Mode: ModeFixed, FixedDutyPercent: 100, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu"},
		3: {Mode: ModeCurve, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu", Curve: Curve{{40, 35}, {60, 55}, {80, 100}}},
	}
	got, err := policies.Duties(map[string]float64{"cpu": 50})
	if err != nil {
		t.Fatal(err)
	}
	if got[2] != 100 || got[3] != 45 {
		t.Fatalf("duties = %v; want fan2=100 and fan3=45", got)
	}
}

func TestPolicyValidationRejectsUnsafeSettings(t *testing.T) {
	validCurve := Curve{{TemperatureC: 40, DutyPercent: 35}, {TemperatureC: 80, DutyPercent: 100}}
	tests := []FanPolicy{
		{Mode: ModeFixed, FixedDutyPercent: 20, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu"},
		{Mode: ModeCurve, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu", Curve: Curve{{60, 50}, {40, 80}}},
		{Mode: ModeCurve, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu", Curve: Curve{{40, 35}, {80, 90}}},
		{Mode: "bios", MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu", Curve: validCurve},
	}
	for _, policy := range tests {
		if err := policy.Validate(); err == nil {
			t.Fatalf("Validate(%+v) returned nil", policy)
		}
	}
	if err := (PolicySet{2: {Mode: ModeFullSpeed}, 5: {Mode: ModeFullSpeed}}).Validate([]int{2, 3}); err == nil {
		t.Fatal("policy for unknown fan channel was accepted")
	}
}

type fakeSensorIO struct {
	values map[string]int
	writes [][2]int
}

func (fake *fakeSensorIO) ReadInt(path string) (int, error) {
	value, ok := fake.values[path]
	if !ok {
		return 0, os.ErrNotExist
	}
	return value, nil
}

func (fake *fakeSensorIO) WriteInt(path string, value int) error {
	fake.values[path] = value
	fake.writes = append(fake.writes, [2]int{len(path), value})
	if path == "/pwm3" && value > 143 {
		fake.values["/fan3"] = 2200
	}
	return nil
}

func TestVerifySoftwareControlObservesRPMAndAlwaysRestores(t *testing.T) {
	fake := &fakeSensorIO{values: map[string]int{"/fan3": 1790, "/pwm3": 143, "/enable3": 1}}
	channel := hardware.FanChannel{Number: 3, RPMPath: "/fan3", PWMPath: "/pwm3", EnablePath: "/enable3"}
	result, err := VerifySoftwareControl(context.Background(), fake, channel, VerifyOptions{Settle: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Supported || result.InitialRPM != 1790 || result.ObservedRPM != 2200 {
		t.Fatalf("result = %+v", result)
	}
	if fake.values["/pwm3"] != 143 || fake.values["/enable3"] != 1 {
		t.Fatalf("state was not restored: %v", fake.values)
	}
}

func TestVerifySoftwareControlDoesNotLowerFullSpeedWithoutConsent(t *testing.T) {
	fake := &fakeSensorIO{values: map[string]int{"/fan2": 3550, "/pwm2": 255, "/enable2": 1}}
	channel := hardware.FanChannel{Number: 2, RPMPath: "/fan2", PWMPath: "/pwm2", EnablePath: "/enable2"}
	_, err := VerifySoftwareControl(context.Background(), fake, channel, VerifyOptions{Settle: func(context.Context) error { return nil }})
	if !errors.Is(err, ErrLoweringConsentRequired) || len(fake.writes) != 0 {
		t.Fatalf("error = %v, writes = %v", err, fake.writes)
	}
}

func TestFanControllerAppliesIndependentPoliciesAndFailsAllOwnedFansSafe(t *testing.T) {
	fake := &fakeSensorIO{values: map[string]int{
		"/fan2": 3550, "/pwm2": 143, "/enable2": 1,
		"/fan3": 1790, "/pwm3": 143, "/enable3": 1,
		"/cpu": 50000,
	}}
	channels := []hardware.FanChannel{
		{Number: 2, RPMPath: "/fan2", PWMPath: "/pwm2", EnablePath: "/enable2", Verified: true},
		{Number: 3, RPMPath: "/fan3", PWMPath: "/pwm3", EnablePath: "/enable3", Verified: true},
	}
	controller := NewFanController(fake, channels, map[string]string{"cpu": "/cpu"})
	policies := PolicySet{
		2: {Mode: ModeFixed, FixedDutyPercent: 100, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu"},
		3: {Mode: ModeCurve, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu", Curve: Curve{{40, 35}, {60, 55}, {80, 100}}},
	}
	if err := controller.Apply(context.Background(), policies); err != nil {
		t.Fatal(err)
	}
	if fake.values["/pwm2"] != 255 || fake.values["/pwm3"] != percentToPWM(45) {
		t.Fatalf("independent policies not applied: %v", fake.values)
	}
	delete(fake.values, "/cpu")
	if err := controller.Apply(context.Background(), policies); !errors.Is(err, ErrTemperatureUnavailable) {
		t.Fatalf("Apply error = %v; want ErrTemperatureUnavailable", err)
	}
	if fake.values["/pwm2"] != 255 || fake.values["/pwm3"] != 255 {
		t.Fatalf("owned fans were not forced to full speed: %v", fake.values)
	}
}

func TestMonitorModeNeverWrites(t *testing.T) {
	fake := &fakeSensorIO{values: map[string]int{"/fan2": 3550, "/pwm2": 143, "/enable2": 1}}
	controller := NewFanController(fake, []hardware.FanChannel{{Number: 2, RPMPath: "/fan2", PWMPath: "/pwm2", EnablePath: "/enable2", Verified: true}}, nil)
	if err := controller.Apply(context.Background(), PolicySet{2: {Mode: ModeMonitor}}); err != nil {
		t.Fatal(err)
	}
	if len(fake.writes) != 0 {
		t.Fatalf("monitor mode wrote hardware: %v", fake.writes)
	}
}
