package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{VendorID: "0204", ProductID: "6025", Serial: "047894467501", ShutdownTimeout: time.Second}
}

func TestNormalizeSerial(t *testing.T) {
	if got := normalizeSerial("  abC-123 "); got != "ABC-123" {
		t.Fatalf("got %q", got)
	}
}

func TestConfigValidation(t *testing.T) {
	valid := testConfig()
	valid.PrePoweroffDelay = 2 * time.Second
	if err := validateConfig(valid); err != nil {
		t.Fatal(err)
	}
	tooLong := valid
	tooLong.ShutdownTimeout = 31 * time.Second
	if err := validateConfig(tooLong); err == nil {
		t.Fatal("expected 30-second maximum rejection")
	}
	cases := []Config{
		{VendorID: "204", ProductID: "6025", Serial: "abc", ShutdownTimeout: time.Second},
		{VendorID: "0204", ProductID: "6025", Serial: "", ShutdownTimeout: time.Second},
		{VendorID: "0204", ProductID: "6025", Serial: "abc", PrePoweroffDelay: -time.Second, ShutdownTimeout: time.Second},
		{VendorID: "0204", ProductID: "6025", Serial: "abc", ShutdownTimeout: 0},
	}
	for i, c := range cases {
		if err := validateConfig(c); err == nil {
			t.Fatalf("case %d unexpectedly valid", i)
		}
	}
}

func TestIdentityMatchingRequiresVIDPIDAndSerial(t *testing.T) {
	c := testConfig()
	cases := []struct {
		name string
		usb  USB
		want bool
	}{
		{"correct", USB{VendorID: "0204", ProductID: "6025", Serial: "047894467501"}, true},
		{"normalized serial", USB{VendorID: "0204", ProductID: "6025", Serial: " 047894467501 "}, true},
		{"wrong vendor", USB{VendorID: "9999", ProductID: "6025", Serial: "047894467501"}, false},
		{"wrong product", USB{VendorID: "0204", ProductID: "9999", Serial: "047894467501"}, false},
		{"wrong serial", USB{VendorID: "0204", ProductID: "6025", Serial: "other"}, false},
		{"missing serial", USB{VendorID: "0204", ProductID: "6025"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matches(tc.usb, c); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidUSBTokenRequiresStableSerial(t *testing.T) {
	cases := []struct {
		name string
		usb  USB
		want bool
	}{
		{"valid", USB{VendorID: "0204", ProductID: "6025", Serial: "047894467501"}, true},
		{"normalized serial", USB{VendorID: "0204", ProductID: "6025", Serial: " 047894467501 "}, true},
		{"missing serial", USB{VendorID: "0204", ProductID: "6025"}, false},
		{"invalid serial", USB{VendorID: "0204", ProductID: "6025", Serial: "contains space"}, false},
		{"invalid vendor", USB{VendorID: "204", ProductID: "6025", Serial: "047894467501"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validUSBToken(tc.usb); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSaveConfigValidatesBeforePersistence(t *testing.T) {
	oldPath := configPath
	configPath = filepath.Join(t.TempDir(), "usbkill", "config.yaml")
	defer func() { configPath = oldPath }()
	invalid := testConfig()
	invalid.Serial = ""
	if err := saveConfig(invalid); err == nil {
		t.Fatal("expected invalid configuration rejection")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config file exists or returned unexpected error: %v", err)
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	oldPath := configPath
	configPath = filepath.Join(t.TempDir(), "usbkill", "config.yaml")
	defer func() { configPath = oldPath }()
	want := testConfig()
	want.PrePoweroffDelay = 2 * time.Second
	if err := saveConfig(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("config = %#v, want %#v", got, want)
	}
}

func TestPrivilegedHelperPathsAreAbsolute(t *testing.T) {
	if systemctlPath != "/usr/bin/systemctl" || udevadmPath != "/usr/bin/udevadm" {
		t.Fatalf("unexpected helper paths: systemctl=%q udevadm=%q", systemctlPath, udevadmPath)
	}
}

func TestWatchdogServiceState(t *testing.T) {
	oldOutput := systemctlOutput
	defer func() { systemctlOutput = oldOutput }()
	systemctlOutput = func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "is-active usbkill.service" {
			t.Fatalf("systemctl arguments = %v", args)
		}
		return []byte("failed\n"), errors.New("inactive")
	}
	if got := watchdogServiceState(); got != "FAILED" {
		t.Fatalf("service state = %q, want FAILED", got)
	}
	systemctlOutput = func(args ...string) ([]byte, error) { return nil, errors.New("dbus unavailable") }
	if got := watchdogServiceState(); got != "UNKNOWN" {
		t.Fatalf("service state = %q, want UNKNOWN", got)
	}
}

func TestStatusReportIncludesServiceHealth(t *testing.T) {
	finder := func(Config) ([]USB, error) { return []USB{{}}, nil }
	report, err := statusReport(testConfig(), finder, true, true, "FAILED")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Watchdog: PRESENT", "Service: FAILED", "Armed: yes", "Boot auto-arm: enabled"} {
		if !strings.Contains(report, want) {
			t.Fatalf("status report missing %q: %s", want, report)
		}
	}
}

func TestPresence(t *testing.T) {
	if got := presence(0); got != "ABSENT" {
		t.Fatalf("zero matches = %q", got)
	}
	if got := presence(1); got != "PRESENT" {
		t.Fatalf("one match = %q", got)
	}
	if got := presence(2); got != "AMBIGUOUS (2 matches)" {
		t.Fatalf("two matches = %q", got)
	}
}

func TestPackageDeclaresFailureAlertDependencyAndValidation(t *testing.T) {
	packageData, err := os.ReadFile("PKGBUILD")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packageData), "'util-linux'") {
		t.Fatal("PKGBUILD does not declare util-linux")
	}
	makeData, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(makeData), "systemd-analyze verify usbkill.service usbkill-failure.service") {
		t.Fatal("Makefile does not validate both systemd units")
	}
}

func TestRemovalEventMatching(t *testing.T) {
	c := testConfig()
	base := map[string]string{"ACTION": "remove", "ID_BUS": "usb", "ID_VENDOR_ID": "0204", "ID_MODEL_ID": "6025", "ID_SERIAL_SHORT": "047894467501"}
	cases := []struct {
		name   string
		mutate func(map[string]string)
		want   bool
	}{
		{"correct", func(map[string]string) {}, true},
		{"normalized serial", func(m map[string]string) { m["ID_SERIAL_SHORT"] = " 047894467501 " }, true},
		{"wrong vendor", func(m map[string]string) { m["ID_VENDOR_ID"] = "9999" }, false},
		{"wrong product", func(m map[string]string) { m["ID_MODEL_ID"] = "9999" }, false},
		{"wrong serial", func(m map[string]string) { m["ID_SERIAL_SHORT"] = "other" }, false},
		{"missing serial", func(m map[string]string) { delete(m, "ID_SERIAL_SHORT") }, false},
		{"non USB", func(m map[string]string) { m["ID_BUS"] = "pci" }, false},
		{"add", func(m map[string]string) { m["ACTION"] = "add" }, false},
		{"change", func(m map[string]string) { m["ACTION"] = "change" }, false},
		{"malformed", func(m map[string]string) { delete(m, "ID_MODEL_ID") }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := make(map[string]string, len(base))
			for k, v := range base {
				m[k] = v
			}
			tc.mutate(m)
			if got := removalMatches(m, c); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCorrectRemovalRequestsShutdownOnce(t *testing.T) {
	input := "ACTION=remove\nID_BUS=usb\nID_VENDOR_ID=0204\nID_MODEL_ID=6025\nID_SERIAL_SHORT=047894467501\n\n"
	mock := &MockPoweroff{}
	if err := monitorReader(context.Background(), testConfig(), slog.Default(), strings.NewReader(input), mock); !errors.Is(err, errShutdownHandled) {
		t.Fatalf("monitor error = %v, want handled shutdown", err)
	}
	if mock.Calls != 1 {
		t.Fatalf("poweroff calls = %d", mock.Calls)
	}
}

func TestTestModeEndedNonMatchingStreamIsNonDestructive(t *testing.T) {
	input := "ACTION=add\nID_BUS=usb\nID_VENDOR_ID=0204\nID_MODEL_ID=6025\nID_SERIAL_SHORT=047894467501\n\nACTION=remove\nID_BUS=usb\nID_VENDOR_ID=9999\nID_MODEL_ID=6025\nID_SERIAL_SHORT=047894467501\n\n"
	mock := &MockPoweroff{}
	if err := monitorReader(context.Background(), testConfig(), slog.Default(), strings.NewReader(input), mock); err == nil {
		t.Fatal("expected test-mode stream error")
	}
	if mock.Calls != 0 {
		t.Fatalf("poweroff calls = %d, want 0", mock.Calls)
	}
}

func TestTestModeUsesMockPoweroff(t *testing.T) {
	mock := &MockPoweroff{}
	if err := runShutdown(context.Background(), testConfig(), slog.Default(), mock); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatal("mock poweroff was not called")
	}
}

func TestShutdownDelayCancellation(t *testing.T) {
	c := testConfig()
	c.PrePoweroffDelay = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mock := &MockPoweroff{}
	if err := runShutdown(ctx, c, slog.Default(), mock); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 0 {
		t.Fatal("poweroff called after cancellation")
	}
}

func TestShutdownFailureReturned(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	mock := &MockPoweroff{Err: errors.New("poweroff failed")}
	if err := runShutdown(context.Background(), testConfig(), logger, mock); err == nil {
		t.Fatal("expected failure")
	}
	if mock.Calls != maxPoweroffAttempts {
		t.Fatalf("calls = %d, want %d", mock.Calls, maxPoweroffAttempts)
	}
	if !strings.Contains(logs.String(), "shutdown attempts exhausted") {
		t.Fatal("missing shutdown exhaustion log")
	}
}

func TestShutdownTimeoutIsBounded(t *testing.T) {
	c := testConfig()
	c.ShutdownTimeout = 10 * time.Millisecond
	mock := &blockingPoweroff{}
	start := time.Now()
	if err := runShutdown(context.Background(), c, slog.Default(), mock); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("shutdown exceeded practical bound")
	}
}

type blockingPoweroff struct{}

type successfulPoweroff struct {
	Calls int
}

func (*blockingPoweroff) Run(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

func (p *successfulPoweroff) Run(context.Context) error {
	p.Calls++
	return nil
}

func TestMonitorReaderFailureFailsClosed(t *testing.T) {
	poweroff := &successfulPoweroff{}
	if err := monitorReader(context.Background(), testConfig(), slog.Default(), &errorReader{}, poweroff); !errors.Is(err, errShutdownHandled) {
		t.Fatalf("monitor error = %v, want handled shutdown", err)
	}
	if poweroff.Calls != 1 {
		t.Fatalf("poweroff calls = %d, want 1", poweroff.Calls)
	}
}

func TestMonitorReaderEOFFailsClosed(t *testing.T) {
	poweroff := &successfulPoweroff{}
	if err := monitorReader(context.Background(), testConfig(), slog.Default(), strings.NewReader(""), poweroff); !errors.Is(err, errShutdownHandled) {
		t.Fatalf("monitor error = %v, want handled shutdown", err)
	}
	if poweroff.Calls != 1 {
		t.Fatalf("poweroff calls = %d, want 1", poweroff.Calls)
	}
}

func TestTestModeMonitorReaderErrorsAreNonDestructive(t *testing.T) {
	cases := []io.Reader{&errorReader{}, strings.NewReader("")}
	for _, reader := range cases {
		mock := &MockPoweroff{}
		if err := monitorReader(context.Background(), testConfig(), slog.Default(), reader, mock); err == nil {
			t.Fatal("expected test-mode monitor error")
		}
		if mock.Calls != 0 {
			t.Fatalf("poweroff calls = %d, want 0", mock.Calls)
		}
	}
}

func TestBootAutoarmArmsOnlyForExactlyOneToken(t *testing.T) {
	oldArmedPath, oldSystemctlRun := armedPath, systemctlRun
	armedPath = filepath.Join(t.TempDir(), "armed")
	defer func() {
		armedPath = oldArmedPath
		systemctlRun = oldSystemctlRun
	}()
	var calls [][]string
	systemctlRun = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	finder := func(Config) ([]USB, error) { return []USB{{}}, nil }
	if err := bootAutoarmConfig(testConfig(), finder); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(armedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("armed marker mode = %o, want 0600", info.Mode().Perm())
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "enable --now usbkill.service" {
		t.Fatalf("systemctl calls = %#v", calls)
	}
}

func TestBootAutoarmLeavesDisarmedForUnsafeTokenState(t *testing.T) {
	oldArmedPath, oldSystemctlRun := armedPath, systemctlRun
	armedPath = filepath.Join(t.TempDir(), "armed")
	defer func() {
		armedPath = oldArmedPath
		systemctlRun = oldSystemctlRun
	}()
	systemctlRun = func(args ...string) error {
		t.Fatalf("unexpected systemctl call: %v", args)
		return nil
	}
	finders := []deviceFinder{
		func(Config) ([]USB, error) { return nil, nil },
		func(Config) ([]USB, error) { return []USB{{}, {}}, nil },
	}
	for _, finder := range finders {
		if err := bootAutoarmConfig(testConfig(), finder); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(armedPath); !os.IsNotExist(err) {
			t.Fatalf("armed marker exists or returned unexpected error: %v", err)
		}
	}
}

func TestBootAutoarmDiscoveryFailureDoesNotArm(t *testing.T) {
	oldArmedPath, oldSystemctlRun := armedPath, systemctlRun
	armedPath = filepath.Join(t.TempDir(), "armed")
	defer func() {
		armedPath = oldArmedPath
		systemctlRun = oldSystemctlRun
	}()
	systemctlRun = func(args ...string) error {
		t.Fatalf("unexpected systemctl call: %v", args)
		return nil
	}
	finder := func(Config) ([]USB, error) { return nil, errors.New("udev unavailable") }
	if err := bootAutoarmConfig(testConfig(), finder); err == nil {
		t.Fatal("expected discovery failure")
	}
	if _, err := os.Stat(armedPath); !os.IsNotExist(err) {
		t.Fatalf("armed marker exists or returned unexpected error: %v", err)
	}
}

func TestAutoarmTogglePersistsOptInAndControlsUnit(t *testing.T) {
	oldConfigPath, oldAutoArmPath, oldSystemctlRun := configPath, autoArmPath, systemctlRun
	temp := t.TempDir()
	configPath = filepath.Join(temp, "config.yaml")
	autoArmPath = filepath.Join(temp, "auto-arm")
	defer func() {
		configPath = oldConfigPath
		autoArmPath = oldAutoArmPath
		systemctlRun = oldSystemctlRun
	}()
	if err := saveConfig(testConfig()); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	systemctlRun = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	if err := enableAutoarm(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(autoArmPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("auto-arm marker mode = %o, want 0600", info.Mode().Perm())
	}
	if err := disableAutoarm(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(autoArmPath); !os.IsNotExist(err) {
		t.Fatalf("auto-arm marker exists or returned unexpected error: %v", err)
	}
	if got := []string{strings.Join(calls[0], " "), strings.Join(calls[1], " ")}; strings.Join(got, "|") != "enable usbkill-autoarm.service|disable --now usbkill-autoarm.service" {
		t.Fatalf("systemctl calls = %#v", calls)
	}
}

func TestVerifyStartupTokenFailsClosedInProduction(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := []struct {
		name   string
		finder deviceFinder
	}{
		{"discovery error", func(Config) ([]USB, error) { return nil, errors.New("udev unavailable") }},
		{"absent", func(Config) ([]USB, error) { return nil, nil }},
		{"ambiguous", func(Config) ([]USB, error) { return []USB{{}, {}}, nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &MockPoweroff{}
			if err := verifyStartupToken(context.Background(), testConfig(), false, logger, mock, tc.finder); !errors.Is(err, errShutdownHandled) {
				t.Fatalf("startup error = %v, want handled shutdown", err)
			}
			if mock.Calls != 1 {
				t.Fatalf("poweroff calls = %d, want 1", mock.Calls)
			}
		})
	}
}

func TestVerifyStartupTokenIsNonDestructiveInTestMode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mock := &MockPoweroff{}
	finder := func(Config) ([]USB, error) { return nil, errors.New("udev unavailable") }
	if err := verifyStartupToken(context.Background(), testConfig(), true, logger, mock, finder); err == nil {
		t.Fatal("expected test-mode discovery error")
	}
	if mock.Calls != 0 {
		t.Fatalf("poweroff calls = %d, want 0", mock.Calls)
	}
}

func TestVerifyStartupTokenAcceptsExactlyOneMatch(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mock := &MockPoweroff{}
	finder := func(Config) ([]USB, error) { return []USB{{}}, nil }
	if err := verifyStartupToken(context.Background(), testConfig(), false, logger, mock, finder); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 0 {
		t.Fatalf("poweroff calls = %d, want 0", mock.Calls)
	}
}

func TestNotifyReadySendsSystemdMessage(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "notify.sock")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("NOTIFY_SOCKET", socket)
	notifyReady(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, _, err := listener.ReadFromUnix(buf)
	if err != nil {
		t.Fatal(err)
	}
	message := string(buf[:n])
	if !strings.Contains(message, "READY=1") || !strings.Contains(message, "STATUS=USB token monitor active") {
		t.Fatalf("unexpected readiness message %q", message)
	}
}

func TestServicePreservesArmedStateAndUsesSingleBoundedFailurePolicy(t *testing.T) {
	data, err := os.ReadFile("usbkill.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	for _, want := range []string{"Type=notify", "NotifyAccess=main", "TimeoutStartSec=30s", "Restart=no", "RuntimeDirectoryPreserve=restart", "OnFailure=usbkill-failure.service", "NoNewPrivileges=yes", "CapabilityBoundingSet=", "ProtectSystem=strict", "ProtectClock=yes", "ProtectHostname=yes", "ProtectProc=invisible", "ProcSubset=pid", "RestrictNamespaces=yes", "RestrictRealtime=yes", "MemoryDenyWriteExecute=yes"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("service unit missing %q", want)
		}
	}
	for _, forbidden := range []string{"Restart=on-failure", "StartLimitIntervalSec=", "StartLimitBurst="} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("service unit contains obsolete restart policy %q", forbidden)
		}
	}
}

type fakeMonitor struct {
	killed  bool
	waited  bool
	waitErr error
}

func (m *fakeMonitor) kill() error {
	m.killed = true
	return nil
}

func (m *fakeMonitor) wait() error {
	m.waited = true
	return m.waitErr
}

func TestMonitorSubscribesBeforeStartupCheck(t *testing.T) {
	oldStartMonitor := startMonitor
	defer func() { startMonitor = oldStartMonitor }()
	process := &fakeMonitor{}
	order := []string{}
	startMonitor = func(context.Context) (io.Reader, monitorHandle, error) {
		order = append(order, "monitor")
		return strings.NewReader(""), process, nil
	}
	startup := func() error {
		order = append(order, "startup")
		return errors.New("stop after startup check")
	}
	if err := monitor(context.Background(), testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)), &MockPoweroff{}, startup, func() { order = append(order, "ready") }); err == nil {
		t.Fatal("expected startup failure")
	}
	if strings.Join(order, ",") != "monitor,startup" {
		t.Fatalf("order = %v", order)
	}
	if !process.killed || !process.waited {
		t.Fatalf("monitor cleanup killed=%v waited=%v", process.killed, process.waited)
	}
}

func TestMonitorReturnsSuccessAfterStartupShutdown(t *testing.T) {
	oldStartMonitor := startMonitor
	defer func() { startMonitor = oldStartMonitor }()
	process := &fakeMonitor{waitErr: errors.New("signal: killed")}
	startMonitor = func(context.Context) (io.Reader, monitorHandle, error) {
		return strings.NewReader(""), process, nil
	}
	ready := false
	if err := monitor(context.Background(), testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)), &MockPoweroff{}, func() error { return errShutdownHandled }, func() { ready = true }); err != nil {
		t.Fatalf("monitor error = %v, want nil", err)
	}
	if ready || !process.killed || !process.waited {
		t.Fatalf("ready=%v killed=%v waited=%v", ready, process.killed, process.waited)
	}
}

func TestMonitorReturnsSuccessAfterHandledShutdown(t *testing.T) {
	oldStartMonitor := startMonitor
	defer func() { startMonitor = oldStartMonitor }()
	process := &fakeMonitor{waitErr: errors.New("signal: killed")}
	startMonitor = func(context.Context) (io.Reader, monitorHandle, error) {
		return strings.NewReader("ACTION=remove\nID_BUS=usb\nID_VENDOR_ID=0204\nID_MODEL_ID=6025\nID_SERIAL_SHORT=047894467501\n\n"), process, nil
	}
	mock := &MockPoweroff{}
	ready := false
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := monitor(context.Background(), testConfig(), logger, mock, func() error { return nil }, func() { ready = true }); err != nil {
		t.Fatalf("monitor error = %v, want nil", err)
	}
	if mock.Calls != 1 {
		t.Fatalf("poweroff calls = %d, want 1", mock.Calls)
	}
	if !ready || !process.killed || !process.waited {
		t.Fatalf("ready=%v killed=%v waited=%v", ready, process.killed, process.waited)
	}
}

func TestTestModeShutdownLogsSuppression(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	mock := &MockPoweroff{}
	if err := runShutdown(context.Background(), testConfig(), logger, mock); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatalf("poweroff calls = %d, want 1", mock.Calls)
	}
	if !strings.Contains(logs.String(), "TEST MODE: poweroff suppressed") {
		t.Fatalf("missing test-mode suppression log: %s", logs.String())
	}
	if strings.Contains(logs.String(), "shutdown command executed") {
		t.Fatalf("test-mode log misreported a real shutdown: %s", logs.String())
	}
}

func TestAutoarmUnitIsOptInAndWatchdogIsArmedGated(t *testing.T) {
	autoarm, err := os.ReadFile("usbkill-autoarm.service")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ConditionPathExists=/etc/usbkill/auto-arm", "After=systemd-udev-settle.service", "ExecStart=/usr/bin/usbkill boot-autoarm", "NoNewPrivileges=yes", "CapabilityBoundingSet=", "RestrictNamespaces=yes", "MemoryDenyWriteExecute=yes"} {
		if !strings.Contains(string(autoarm), want) {
			t.Fatalf("auto-arm unit missing %q", want)
		}
	}
	watchdog, err := os.ReadFile("usbkill.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(watchdog), "ConditionPathExists=/run/usbkill/armed") {
		t.Fatal("watchdog unit is not gated by the armed marker")
	}
}

func TestFailureUnitUsesReducedPrivileges(t *testing.T) {
	data, err := os.ReadFile("usbkill-failure.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	for _, want := range []string{"NoNewPrivileges=yes", "CapabilityBoundingSet=", "ProtectSystem=strict", "RestrictNamespaces=yes", "MemoryDenyWriteExecute=yes", "RestrictAddressFamilies=AF_UNIX"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("failure unit missing %q", want)
		}
	}
}

func TestMonitorLockIsExclusive(t *testing.T) {
	oldPath := lockPath
	lockPath = t.TempDir() + "/monitor.lock"
	defer func() { lockPath = oldPath }()
	first, err := acquireMonitorLock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(first.Fd()), syscall.LOCK_UN); _ = first.Close() }()
	second, err := acquireMonitorLock()
	if err == nil {
		second.Close()
		t.Fatal("expected second monitor lock to fail")
	}
}

type errorReader struct{}

func (*errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
