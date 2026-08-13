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

The `PKGBUILD` is local-source only. After cloning the repository, `makepkg` does not fetch another GitHub source or require a second GitHub login. It installs `/usr/bin/usbkill`, `/usr/lib/systemd/system/usbkill.service`, documentation, and an initially empty `/etc/usbkill` directory. Installation does not configure, enable, or arm the watchdog.

## Configure and test

List unique-serial USB storage devices:

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

After the removal test succeeds and the token is reconnected, arm the current boot session:

```sh
sudo usbkill arm
```

Remove the configured token. Test mode must report the matching removal and suppress the real poweroff. Reconnect the token afterward. Test mode never requires the armed marker. The armed marker lives in `/run`, so it is intentionally cleared on reboot; re-arm after every boot.

Enable production monitoring only after test mode succeeds:

```sh
sudo systemctl enable --now usbkill.service
sudo usbkill status
journalctl -u usbkill.service -f
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

`pre_poweroff_delay` is only a delay. It is **not** RAM sanitization and must not be interpreted as a memory wipe. The default is zero. The configuration also bounds the shutdown command timeout.

The daemon treats the first matching removal as authoritative and returns after one shutdown attempt. Reconnects do not cancel an already scheduled shutdown. Malformed, unrelated, add, change, wrong-VID, wrong-PID, wrong-serial, non-USB, and missing-serial events are ignored.

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

A privileged attacker can disable the service, alter the binary or configuration, or boot another operating system. Normal poweroff is used instead of an abrupt hard power cut because filesystem integrity matters.

## Recovery

If the token is lost or the service repeatedly powers off the machine, boot an Arch ISO, mount the installed root filesystem, and disable the unit:

```sh
mount /dev/ROOT_PARTITION /mnt
arch-chroot /mnt systemctl disable usbkill.service
rm -f /mnt/run/usbkill/armed
rm -f /mnt/etc/usbkill/config.yaml
```

The exact root partition and encrypted-volume steps depend on the installation. Do not re-enable the service until the token and configuration have been verified in test mode.

## Development and validation

The project intentionally uses the Go standard library and Arch's systemd/udev tools. It has no networking, custom drivers, initramfs integration, cryptography, or memory-wiping tricks.

```sh
make fmt
make vet
make test
go test .
go test -race .
go build .
```

On Arch Linux, validate and build the package with:

```sh
makepkg --printsrcinfo > .SRCINFO
makepkg -f
```

The package check uses `go test .`, not `go test ./...`, because `makepkg` creates `pkg/` and `src/` directories in the checkout.
