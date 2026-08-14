# usbkill

`usbkill` is a small Arch Linux systemd daemon for a physical-presence USB token. When the exact configured token is removed from an already-running workstation, `usbkill` detects the udev event and requests a normal `systemctl poweroff`.

The USB device is **not** a LUKS key. LUKS remains responsible for encrypting persistent storage, and the LUKS passphrase remains required at the next boot. `usbkill` is defense in depth for a running, already-unlocked workstation.

## Install on Arch Linux

```sh
sudo pacman -Syu --needed git go base-devel systemd
git clone https://github.com/sesjaphp/usbkill.git
cd usbkill
makepkg -si
```

The `PKGBUILD` is local-source only. After cloning the repository, `makepkg` does not fetch another GitHub source or require a second GitHub login. It installs `/usr/bin/usbkill`, `usbkill.service`, `usbkill-failure.service`, and `usbkill-autoarm.service`, documentation, and an initially empty `/etc/usbkill` directory. The package declares `util-linux` because the failure notification uses its `logger` command. Installation does not configure, enable, or arm the watchdog. For command reference, safe boot-auto-arm testing, diagnostics, upgrades, and recovery, read the [operations guide](docs/operations.md).

## Configure and test

List USB storage devices with valid VID, PID, and non-empty stable serial identities:

```sh
sudo usbkill list
```

Configure the selected token. Devices without a serial number are rejected:

```sh
sudo usbkill setup
```

Run safe test mode before arming:

```sh
sudo usbkill test
```

After the removal test succeeds and the token is reconnected, arm the current boot session. `arm` writes the transient armed marker and enables/starts the production systemd service. It returns success only after the daemon has started the udev monitor and reported readiness to systemd. That readiness must complete within 30 seconds; otherwise arming fails instead of waiting indefinitely:

```sh
sudo usbkill arm
```

Remove the configured token. A successful test must report the matching removal followed by `TEST MODE: poweroff suppressed`, then exit without an error. Test mode never invokes a real poweroff. Reconnect the token afterward. If it reports `TEST MODE: udev monitor stream ...; poweroff suppressed`, no poweroff occurred, but the test failed and must be investigated before arming. Test mode never requires the armed marker. The armed marker lives in `/run`, so it is intentionally cleared on reboot. The watchdog unit is skipped rather than failed while this marker is absent. Reconnecting the token after a shutdown does not cancel an already-triggered shutdown.

Check production monitoring after arming:

```sh
sudo usbkill status
journalctl -u usbkill.service -f
journalctl -u usbkill-failure.service -b --no-pager
```

`status` reports `PRESENT`, `ABSENT`, or `AMBIGUOUS (n matches)` for the configured identity, plus the live systemd state. `Armed: yes` records the intended watchdog state; `Service: ACTIVE` confirms the monitor is currently running. An ambiguous state or a non-active service must be investigated before arming.

### Optional boot auto-arm

After you have passed the non-destructive test and deliberately want the watchdog to re-arm on future boots, enable the explicit opt-in setting:

```sh
sudo usbkill enable-autoarm
```

At each boot, the one-shot auto-arm unit waits for udev settlement and arms only if exactly one configured token is present. If the token is absent, ambiguous, or discovery fails, it leaves the watchdog disarmed and records the reason; it does not power off the machine. Check the setting with `sudo usbkill status`. Disable future boot auto-arming without disarming the current session using:

```sh
sudo usbkill disable-autoarm
```

`disarm` stops and disables the production service before removing the marker:

```sh
sudo usbkill disarm
```

Disable the trigger with:

```sh
sudo systemctl disable --now usbkill.service
sudo usbkill disarm
```

## Behavior

The security path is:

```text
USB token removed
    -> udev removal event
    -> exact VID/PID/serial match
    -> optional pre-poweroff delay
    -> bounded systemctl poweroff
```

`pre_poweroff_delay` is only a delay. It is **not** RAM sanitization and must not be interpreted as a memory wipe. The default is zero. The configuration also bounds the shutdown command timeout to 30 seconds; setup uses a 10-second default. If the shutdown command fails, usbkill logs the failure and retries up to three times with a one-second gap. If all attempts fail, the daemon returns failure to systemd without an automatic service restart, and `usbkill-failure.service` writes an `authpriv.alert` journal record. The armed runtime directory is preserved across a service restart, while an explicit stop or reboot still clears the transient armed marker. An armed service fails closed at startup: if the configured token is absent or the device match is ambiguous, it enters the same bounded shutdown path instead of merely exiting.

The daemon starts the udev monitor before its final startup token verification, so removals that occur during that verification remain queued for event matching. The daemon treats the first matching removal as authoritative and runs one bounded shutdown sequence; that sequence may contain up to three controlled attempts. Reconnects do not cancel it. An exclusive runtime lock prevents test mode and the production daemon from monitoring concurrently; test mode also refuses to run while the production service is active. Reconnects do not cancel an already scheduled shutdown. Malformed, unrelated, add, change, wrong-VID, wrong-PID, wrong-serial, non-USB, and missing-serial events are ignored.

## Configuration

The root-owned file is `/etc/usbkill/config.yaml`:

```yaml
vendor_id: "0204"
product_id: "6025"
serial: "047894467501"
pre_poweroff_delay: 0s
shutdown_timeout: 10s
```

The file is written atomically with mode `0600`; group- or world-writable configuration is rejected. IDs, serials, durations, unknown fields, and duplicate fields are validated. Serial values are normalized to uppercase and trimmed before comparison.

## Security model and limitations

### Protected

`usbkill` is designed to reduce exposure after accidental or deliberate removal of the configured token from a running workstation. It helps against opportunistic physical access after token removal by returning the system to its powered-off state, where LUKS protects persistent storage.

### Not protected

`usbkill` does not guarantee destruction of DRAM, CPU caches, GPU memory, firmware memory, DMA buffers, or swap. It does not protect against a root attacker, compromised kernel, modified filesystem, firmware or UEFI compromise, cold-boot attack, storage tampering, power removal, or an attacker who prevents the service from starting. LUKS does not protect plaintext that remains available in RAM while the system is running. Early-boot or initramfs protection is a separate project and is not implemented here.

The production watchdog invokes fixed `/usr/bin/systemctl` and `/usr/bin/udevadm` helper paths rather than inherited `PATH` entries. The production watchdog, boot auto-arm, and failure-notification units use `NoNewPrivileges=yes`, empty capability bounding sets, filesystem protections, namespace restrictions, restricted address families, and write-execute memory protection. The daemon still runs as root because it must read root-only configuration and request system-managed poweroff, but its subprocesses cannot gain additional privileges through setuid, setgid, or file-capability execution. The sandbox can verify unit syntax and static exposure but cannot exercise a real systemd poweroff transaction. Every release containing hardening changes must therefore be tested on target Arch hardware with a controlled real token-removal poweroff before the settings are considered operationally proven. A privileged attacker can still disable the service, alter the binary or configuration, or boot another operating system. Normal systemd poweroff is used instead of an abrupt hard power cut because filesystem integrity matters. The kill-switch invocation uses `--ignore-inhibitors` deliberately: active desktop or application poweroff inhibitors must not leave the machine running after the configured token is removed. This can interrupt applications that requested the inhibitor, which is expected emergency behavior.

## Recovery

If the token is lost or the service repeatedly powers off the machine, boot an Arch ISO, mount the installed root filesystem, and disable the unit:

```sh
mount /dev/ROOT_PARTITION /mnt
arch-chroot /mnt systemctl disable usbkill.service usbkill-autoarm.service
rm -f /mnt/run/usbkill/armed
rm -f /mnt/etc/usbkill/auto-arm
rm -f /mnt/etc/usbkill/config.yaml
```

The exact root partition and encrypted-volume steps depend on the installation. Do not re-enable the service until the token and configuration have been verified in test mode.

## Contributing

Please read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request. The repository includes GitHub issue forms, a pull-request checklist, GitHub Actions CI, ownership rules, an [architecture overview](docs/architecture.md), an [operations guide](docs/operations.md), and a [security policy](SECURITY.md). Never publish device serial numbers or exploitable security details in public issues.

## Development and validation

The project intentionally uses the Go standard library and Arch's systemd/udev tools. It has no networking, custom drivers, initramfs integration, cryptography, or memory-wiping tricks.

```sh
make fmt
make vet
make test
go test .
go test -race .
go build .
systemd-analyze verify usbkill.service usbkill-failure.service usbkill-autoarm.service
```

On Arch Linux, validate and build the package with:

```sh
makepkg --printsrcinfo > .SRCINFO
makepkg -f
```

The package check uses `go test .`, not `go test ./...`, because `makepkg` creates `pkg/` and `src/` directories in the checkout.
