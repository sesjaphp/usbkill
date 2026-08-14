package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	armedPath           = "/run/usbkill/armed"
	maxPoweroffAttempts = 3
	poweroffRetryDelay  = time.Second
)

var (
	configPath = "/etc/usbkill/config.yaml"
	lockPath   = "/run/usbkill/monitor.lock"
)

type Config struct {
	VendorID         string
	ProductID        string
	Serial           string
	PrePoweroffDelay time.Duration
	ShutdownTimeout  time.Duration
}

type USB struct {
	Node, Model, VendorID, ProductID, Serial, SysPath string
}

type Poweroff interface {
	Run(context.Context) error
}

type RealPoweroff struct{}

type MockPoweroff struct {
	Calls int
	Err   error
}

func (RealPoweroff) Run(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "systemctl", "--no-ask-password", "--ignore-inhibitors", "poweroff")
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}
	return nil
}

func (m *MockPoweroff) Run(context.Context) error {
	m.Calls++
	return m.Err
}

var idRE = regexp.MustCompile(`^[0-9A-Fa-f]{4}$`)
var serialRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "usbkill:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		usage()
		return errors.New("choose one command")
	}
	switch args[0] {
	case "list":
		return listUSB()
	case "setup":
		return setup()
	case "status":
		return status()
	case "test":
		return daemon(true)
	case "arm":
		return arm()
	case "disarm":
		return disarm()
	case "daemon":
		return daemon(false)
	default:
		usage()
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func usage() { fmt.Println("Usage: usbkill {list|setup|status|test|arm|disarm|daemon}") }

func loadConfig() (Config, error) {
	info, err := os.Stat(configPath)
	if err != nil {
		return Config{}, err
	}
	if info.Mode().Perm()&0022 != 0 {
		return Config{}, errors.New("configuration is writable by group or other users")
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, err
	}
	var c Config
	seen := make(map[string]bool)
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasSuffix(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return Config{}, errors.New("invalid configuration line")
		}
		key, val := strings.TrimSpace(parts[0]), strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if seen[key] {
			return Config{}, fmt.Errorf("duplicate configuration field %q", key)
		}
		seen[key] = true
		switch key {
		case "vendor_id":
			c.VendorID = strings.ToUpper(val)
		case "product_id":
			c.ProductID = strings.ToUpper(val)
		case "serial":
			c.Serial = normalizeSerial(val)
		case "pre_poweroff_delay":
			c.PrePoweroffDelay, err = time.ParseDuration(val)
		case "shutdown_timeout":
			c.ShutdownTimeout, err = time.ParseDuration(val)
		default:
			return Config{}, fmt.Errorf("unknown configuration field %q", key)
		}
		if err != nil {
			return Config{}, fmt.Errorf("invalid %s", key)
		}
	}
	if err := validateConfig(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

func validateConfig(c Config) error {
	if !idRE.MatchString(c.VendorID) || !idRE.MatchString(c.ProductID) {
		return errors.New("vendor_id and product_id must be four hexadecimal characters")
	}
	if !serialRE.MatchString(c.Serial) {
		return errors.New("serial is required and contains invalid characters")
	}
	if c.PrePoweroffDelay < 0 || c.PrePoweroffDelay > time.Minute {
		return errors.New("pre_poweroff_delay must be 0..1m")
	}
	if c.ShutdownTimeout <= 0 || c.ShutdownTimeout > 30*time.Second {
		return errors.New("shutdown_timeout must be 1ns..30s")
	}
	return nil
}

func saveConfig(c Config) error {
	if err := validateConfig(c); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0750); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(configPath), 0750); err != nil {
		return err
	}
	data := fmt.Sprintf("vendor_id: %q\nproduct_id: %q\nserial: %q\npre_poweroff_delay: %s\nshutdown_timeout: %s\n", c.VendorID, c.ProductID, normalizeSerial(c.Serial), c.PrePoweroffDelay, c.ShutdownTimeout)
	tmp, err := os.CreateTemp(filepath.Dir(configPath), ".config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.WriteString(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, configPath)
}

func normalizeSerial(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func printUSBDevices(xs []USB) {
	for i, x := range xs {
		fmt.Printf("[%d] %s\n    Device: %s\n    VID: %s PID: %s Serial: %s\n", i+1, x.Model, x.Node, x.VendorID, x.ProductID, x.Serial)
	}
}

func listUSB() error {
	xs, err := usbDevices()
	if err != nil {
		return err
	}
	if len(xs) == 0 {
		fmt.Println("No USB storage devices found.")
		return nil
	}
	printUSBDevices(xs)
	return nil
}

func usbDevices() ([]USB, error) {
	entries, err := os.ReadDir("/sys/class/block")
	if err != nil {
		return nil, err
	}
	var out []USB
	for _, e := range entries {
		n := e.Name()
		base := filepath.Join("/sys/class/block", n)
		if _, err := os.Stat(filepath.Join(base, "partition")); err == nil {
			continue
		}
		if strings.HasPrefix(n, "loop") || strings.HasPrefix(n, "ram") || strings.HasPrefix(n, "dm-") {
			continue
		}
		props, err := udevProperties(base)
		if err != nil || props["ID_BUS"] != "usb" || props["ID_TYPE"] == "partition" {
			continue
		}
		x := USB{Node: "/dev/" + n, SysPath: base, VendorID: strings.ToUpper(props["ID_VENDOR_ID"]), ProductID: strings.ToUpper(props["ID_MODEL_ID"]), Serial: normalizeSerial(props["ID_SERIAL_SHORT"]), Model: props["ID_MODEL"]}
		if x.Model == "" {
			x.Model = props["ID_MODEL_FROM_DATABASE"]
		}
		if x.Model == "" {
			x.Model = n
		}
		if validUSBToken(x) {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out, nil
}

func udevProperties(sysPath string) (map[string]string, error) {
	cmd := exec.Command("udevadm", "info", "--query=property", "--path", sysPath)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	props := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			props[key] = value
		}
	}
	return props, nil
}

func validUSBToken(x USB) bool {
	return idRE.MatchString(x.VendorID) && idRE.MatchString(x.ProductID) && serialRE.MatchString(normalizeSerial(x.Serial))
}

func matches(x USB, c Config) bool {
	return strings.EqualFold(x.VendorID, c.VendorID) && strings.EqualFold(x.ProductID, c.ProductID) && normalizeSerial(x.Serial) == normalizeSerial(c.Serial) && normalizeSerial(x.Serial) != ""
}

func find(c Config) ([]USB, error) {
	xs, err := usbDevices()
	if err != nil {
		return nil, err
	}
	var m []USB
	for _, x := range xs {
		if matches(x, c) {
			m = append(m, x)
		}
	}
	return m, nil
}

func setup() error {
	xs, err := usbDevices()
	if err != nil {
		return err
	}
	if len(xs) == 0 {
		return errors.New("no unique-serial USB storage devices found")
	}
	printUSBDevices(xs)
	var n int
	fmt.Printf("Select token [1-%d]: ", len(xs))
	if _, err := fmt.Scan(&n); err != nil || n < 1 || n > len(xs) {
		return errors.New("invalid selection")
	}
	x := xs[n-1]
	fmt.Printf("Selected %s VID:%s PID:%s Serial:%s\n", x.Model, x.VendorID, x.ProductID, x.Serial)
	fmt.Print("Removing this token while armed will power off this machine. Continue? [y/N]: ")
	var yes string
	fmt.Scan(&yes)
	if strings.ToLower(yes) != "y" {
		return errors.New("cancelled")
	}
	c := Config{x.VendorID, x.ProductID, normalizeSerial(x.Serial), 0, 10 * time.Second}
	if err := saveConfig(c); err != nil {
		return err
	}
	fmt.Println("Saved configuration. Run: sudo usbkill test, then sudo usbkill arm.")
	return nil
}

func arm() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	m, err := find(c)
	if err != nil {
		return err
	}
	if len(m) != 1 {
		return errors.New("refusing to arm: token absent or ambiguous")
	}
	if err := os.MkdirAll(filepath.Dir(armedPath), 0750); err != nil {
		return err
	}
	if err := os.WriteFile(armedPath, []byte("armed\n"), 0600); err != nil {
		return err
	}
	if err := systemctl("enable", "--now", "usbkill.service"); err != nil {
		_ = os.Remove(armedPath)
		return fmt.Errorf("armed marker written but service could not start: %w", err)
	}
	fmt.Println("Armed and service enabled.")
	return nil
}

func disarm() error {
	if err := systemctl("disable", "--now", "usbkill.service"); err != nil {
		return fmt.Errorf("could not stop and disable service: %w", err)
	}
	if err := os.Remove(armedPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("Disarmed and service disabled.")
	return nil
}

func systemctl(args ...string) error {
	return exec.Command("systemctl", args...).Run()
}

func acquireMonitorLock() (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another usbkill monitor is already running")
	}
	return f, nil
}

func status() error {
	c, err := loadConfig()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	m, err := find(c)
	if err != nil {
		return err
	}
	_, armedErr := os.Stat(armedPath)
	fmt.Printf("Config: valid\nToken: %s\nWatchdog: %s\nPre-poweroff delay: %s\n", c.Serial, presence(len(m)), c.PrePoweroffDelay)
	if armedErr == nil {
		fmt.Println("Armed: yes")
	} else {
		fmt.Println("Armed: no")
	}
	return nil
}

func presence(n int) string {
	switch n {
	case 0:
		return "ABSENT"
	case 1:
		return "PRESENT"
	default:
		return fmt.Sprintf("AMBIGUOUS (%d matches)", n)
	}
}

type deviceFinder func(Config) ([]USB, error)

func daemon(test bool) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if test {
		if err := exec.Command("systemctl", "is-active", "--quiet", "usbkill.service").Run(); err == nil {
			return errors.New("production service is active; run sudo usbkill disarm before test mode")
		}
	} else if _, err = os.Stat(armedPath); err != nil {
		return errors.New("not armed; run sudo usbkill arm")
	}
	var poweroff Poweroff = RealPoweroff{}
	if test {
		poweroff = &MockPoweroff{}
	}
	lock, err := acquireMonitorLock()
	if err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); _ = lock.Close() }()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := verifyStartupToken(ctx, c, test, logger, poweroff, find); err != nil {
		return err
	}
	logger.Info("usb token detected", "test_mode", test)
	return monitor(ctx, c, logger, poweroff, func() { notifyReady(logger) })
}

func verifyStartupToken(ctx context.Context, c Config, test bool, logger *slog.Logger, poweroff Poweroff, finder deviceFinder) error {
	m, err := finder(c)
	if err != nil {
		if test {
			return fmt.Errorf("token discovery failed: %w", err)
		}
		logger.Error("token discovery failed at startup; failing closed", "error", err)
		return runShutdown(ctx, c, logger, poweroff)
	}
	if len(m) == 1 {
		return nil
	}
	if test {
		return errors.New("configured token absent or ambiguous at startup")
	}
	logger.Error("configured token absent or ambiguous at startup; failing closed", "matches", len(m))
	return runShutdown(ctx, c, logger, poweroff)
}

func removalMatches(props map[string]string, c Config) bool {
	if props["ACTION"] != "remove" || props["ID_BUS"] != "usb" {
		return false
	}
	if !strings.EqualFold(props["ID_VENDOR_ID"], c.VendorID) || !strings.EqualFold(props["ID_MODEL_ID"], c.ProductID) {
		return false
	}
	serial := normalizeSerial(props["ID_SERIAL_SHORT"])
	return serial != "" && serial == normalizeSerial(c.Serial)
}

func notifyReady(logger *slog.Logger) {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return
	}
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + socket[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		logger.Error("systemd readiness notification failed", "error", err)
		return
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("READY=1\nSTATUS=USB token monitor active")); err != nil {
		logger.Error("systemd readiness notification failed", "error", err)
	}
}

func monitor(ctx context.Context, c Config, logger *slog.Logger, poweroff Poweroff, ready func()) error {
	cmd := exec.CommandContext(ctx, "udevadm", "monitor", "--udev", "--property")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("start udev monitor: %w", err)
	}
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start udev monitor: %w", err)
	}
	ready()
	monitorErr := monitorReader(ctx, c, logger, stdout, poweroff)
	if ctx.Err() == nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if monitorErr != nil {
		return monitorErr
	}
	if waitErr != nil {
		return fmt.Errorf("udev monitor exited: %w", waitErr)
	}
	return errors.New("udev monitor exited unexpectedly")
}

func monitorReader(ctx context.Context, c Config, logger *slog.Logger, reader io.Reader, poweroff Poweroff) error {
	scanner := bufio.NewScanner(reader)
	props := make(map[string]string)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if removalMatches(props, c) {
				logger.Warn("USB token removed; shutdown scheduled")
				return runShutdown(ctx, c, logger, poweroff)
			}
			props = make(map[string]string)
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := scanner.Err(); err != nil {
		logger.Error("udev monitor stream failed; failing closed", "error", err)
		return runShutdown(ctx, c, logger, poweroff)
	}
	logger.Error("udev monitor stream ended unexpectedly; failing closed")
	return runShutdown(ctx, c, logger, poweroff)
}

func runShutdown(ctx context.Context, c Config, logger *slog.Logger, poweroff Poweroff) error {
	if c.PrePoweroffDelay > 0 {
		timer := time.NewTimer(c.PrePoweroffDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			logger.Warn("shutdown cancelled by service termination")
			return nil
		}
	}
	var lastErr error
	for attempt := 1; attempt <= maxPoweroffAttempts; attempt++ {
		pctx, cancel := context.WithTimeout(ctx, c.ShutdownTimeout)
		err := poweroff.Run(pctx)
		cancel()
		if err == nil {
			logger.Info("shutdown command executed", "attempt", attempt)
			return nil
		}
		lastErr = err
		logger.Error("shutdown command failed", "attempt", attempt, "max_attempts", maxPoweroffAttempts, "error", err)
		if attempt < maxPoweroffAttempts {
			timer := time.NewTimer(poweroffRetryDelay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
	}
	logger.Error("shutdown attempts exhausted; returning failure to systemd", "attempts", maxPoweroffAttempts, "error", lastErr)
	return fmt.Errorf("shutdown failed after %d attempts: %w", maxPoweroffAttempts, lastErr)
}
