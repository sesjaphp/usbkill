package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
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
	if err := monitorReader(context.Background(), testConfig(), slog.Default(), strings.NewReader(input), mock); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatalf("poweroff calls = %d", mock.Calls)
	}
}

func TestEndedNonMatchingStreamFailsClosed(t *testing.T) {
	input := "ACTION=add\nID_BUS=usb\nID_VENDOR_ID=0204\nID_MODEL_ID=6025\nID_SERIAL_SHORT=047894467501\n\nACTION=remove\nID_BUS=usb\nID_VENDOR_ID=9999\nID_MODEL_ID=6025\nID_SERIAL_SHORT=047894467501\n\n"
	mock := &MockPoweroff{}
	if err := monitorReader(context.Background(), testConfig(), slog.Default(), strings.NewReader(input), mock); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatalf("poweroff calls = %d, want 1", mock.Calls)
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

func (*blockingPoweroff) Run(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

func TestMonitorReaderFailureFailsClosed(t *testing.T) {
	mock := &MockPoweroff{}
	if err := monitorReader(context.Background(), testConfig(), slog.Default(), &errorReader{}, mock); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatalf("poweroff calls = %d, want 1", mock.Calls)
	}
}

func TestMonitorReaderEOFFailsClosed(t *testing.T) {
	mock := &MockPoweroff{}
	if err := monitorReader(context.Background(), testConfig(), slog.Default(), strings.NewReader(""), mock); err != nil {
		t.Fatal(err)
	}
	if mock.Calls != 1 {
		t.Fatalf("poweroff calls = %d, want 1", mock.Calls)
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
			if err := verifyStartupToken(context.Background(), testConfig(), false, logger, mock, tc.finder); err != nil {
				t.Fatal(err)
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
