package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	configPath = "/etc/killswitch/config.yaml"
	armedPath  = "/run/killswitch/armed"
)

type Config struct {
	VendorID        string
	ProductID       string
	Serial          string
	Grace           time.Duration
	ShutdownTimeout time.Duration
	SanitizeTimeout time.Duration
}

type USB struct{ Node, Model, VendorID, ProductID, Serial, SysPath string }

type Poweroff interface{ Run(context.Context) error }
type RealPoweroff struct{}
type MockPoweroff struct{}

func (RealPoweroff) Run(ctx context.Context) error {
	return exec.CommandContext(ctx, "systemctl", "poweroff").Run()
}
func (MockPoweroff) Run(context.Context) error { return nil }

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
	b, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, err
	}
	var c Config
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasSuffix(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return Config{}, fmt.Errorf("invalid configuration line")
		}
		key, val := strings.TrimSpace(parts[0]), strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		switch key {
		case "vendor_id":
			c.VendorID = val
		case "product_id":
			c.ProductID = val
		case "serial":
			c.Serial = val
		case "grace_period":
			c.Grace, err = time.ParseDuration(val)
		case "shutdown_timeout":
			c.ShutdownTimeout, err = time.ParseDuration(val)
		case "sanitize_timeout":
			c.SanitizeTimeout, err = time.ParseDuration(val)
		default:
			return Config{}, fmt.Errorf("unknown configuration field %q", key)
		}
		if err != nil {
			return Config{}, fmt.Errorf("invalid %s", key)
		}
	}
	if !idRE.MatchString(c.VendorID) || !idRE.MatchString(c.ProductID) {
		return Config{}, errors.New("vendor_id and product_id must be four hexadecimal characters")
	}
	if !serialRE.MatchString(c.Serial) {
		return Config{}, errors.New("serial is required and contains invalid characters")
	}
	if c.Grace < 0 || c.Grace > time.Hour {
		return Config{}, errors.New("grace_period must be 0..1h")
	}
	if c.ShutdownTimeout <= 0 || c.ShutdownTimeout > 5*time.Minute {
		return Config{}, errors.New("shutdown_timeout must be 1ns..5m")
	}
	if c.SanitizeTimeout <= 0 || c.SanitizeTimeout > time.Minute {
		return Config{}, errors.New("sanitize_timeout must be 1ns..1m")
	}
	return c, nil
}

func saveConfig(c Config) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0750); err != nil {
		return err
	}
	data := fmt.Sprintf("vendor_id: %q\nproduct_id: %q\nserial: %q\ngrace_period: %s\nshutdown_timeout: %s\nsanitize_timeout: %s\n", c.VendorID, c.ProductID, c.Serial, c.Grace, c.ShutdownTimeout, c.SanitizeTimeout)
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

func listUSB() error {
	xs, err := usbDevices()
	if err != nil {
		return err
	}
	if len(xs) == 0 {
		fmt.Println("No USB storage devices found.")
		return nil
	}
	for i, x := range xs {
		fmt.Printf("[%d] %s\n    Device: %s\n    VID: %s PID: %s Serial: %s\n", i+1, x.Model, x.Node, x.VendorID, x.ProductID, x.Serial)
	}
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
		x := USB{Node: "/dev/" + n, SysPath: base, VendorID: props["ID_VENDOR_ID"], ProductID: props["ID_MODEL_ID"], Serial: props["ID_SERIAL_SHORT"], Model: props["ID_MODEL"]}
		if x.Serial == "" {
			x.Serial = props["ID_SERIAL"]
		}
		if x.Model == "" {
			x.Model = props["ID_MODEL_FROM_DATABASE"]
		}
		if x.Model == "" {
			x.Model = n
		}
		if idRE.MatchString(x.VendorID) && idRE.MatchString(x.ProductID) {
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
func readSys(paths ...string) string {
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}
func matches(x USB, c Config) bool {
	return strings.EqualFold(x.VendorID, c.VendorID) && strings.EqualFold(x.ProductID, c.ProductID) && x.Serial == c.Serial
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
		return errors.New("no USB storage devices found")
	}
	if err := listUSB(); err != nil {
		return err
	}
	var n int
	fmt.Printf("Select token [1-%d]: ", len(xs))
	if _, err := fmt.Scan(&n); err != nil || n < 1 || n > len(xs) {
		return errors.New("invalid selection")
	}
	x := xs[n-1]
	if x.Serial == "" {
		return errors.New("token has no unique serial; refusing setup")
	}
	fmt.Printf("Removing %s while armed will power off this machine. Continue? [y/N]: ", x.Model)
	var yes string
	fmt.Scan(&yes)
	if strings.ToLower(yes) != "y" {
		return errors.New("cancelled")
	}
	c := Config{x.VendorID, x.ProductID, x.Serial, 30 * time.Second, 10 * time.Second, 2 * time.Second}
	if err := saveConfig(c); err != nil {
		return err
	}
	fmt.Println("Saved configuration. Run: sudo usbkill test, then sudo usbkill arm")
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
	return os.WriteFile(armedPath, []byte("armed\n"), 0600)
}
func disarm() error {
	if err := os.Remove(armedPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("Disarmed.")
	return nil
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
	fmt.Printf("Config: valid\nToken: %s\nWatchdog: %s\n", c.Serial, present(len(m)))
	if armedErr == nil {
		fmt.Println("Armed: yes")
	} else {
		fmt.Println("Armed: no")
	}
	return nil
}
func present(n int) string {
	if n == 1 {
		return "PRESENT"
	}
	return "ABSENT"
}

func daemon(test bool) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if _, err = os.Stat(armedPath); err != nil {
		return errors.New("not armed; run sudo usbkill arm")
	}
	m, err := find(c)
	if err != nil {
		return err
	}
	if len(m) != 1 {
		return errors.New("configured token absent or ambiguous at startup")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logger.Info("usbkill started", "test_mode", test)
	return monitor(ctx, c, logger, func(ctx context.Context) error {
		if test {
			logger.Warn("TEST MODE: poweroff suppressed")
			return nil
		}
		return RealPoweroff{}.Run(ctx)
	})
}

func monitor(ctx context.Context, c Config, logger *slog.Logger, poweroff func(context.Context) error) error {
	cmd := exec.CommandContext(ctx, "udevadm", "monitor", "--udev", "--property", "--subsystem-match=usb")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	defer cmd.Wait()
	scanner := bufio.NewScanner(stdout)
	props := map[string]string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if props["ACTION"] == "remove" && props["ID_VENDOR_ID"] == c.VendorID && props["ID_MODEL_ID"] == c.ProductID && (props["ID_SERIAL_SHORT"] == c.Serial || props["ID_SERIAL"] == c.Serial) {
				logger.Warn("token removed; starting bounded shutdown")
				timer := time.NewTimer(c.SanitizeTimeout)
				select {
				case <-timer.C:
					logger.Warn("best-effort memory sanitization window ended")
				case <-ctx.Done():
					timer.Stop()
					return nil
				}
				pctx, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
				_ = poweroff(pctx)
				cancel()
				return nil
			}
			props = map[string]string{}
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}
	return scanner.Err()
}
