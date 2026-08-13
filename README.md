# usbkill

`usbkill` is an Arch Linux-only systemd service that powers off the machine when one configured USB storage token is removed. The token is a physical-presence signal, not a LUKS key. LUKS still protects the disk and still requires its passphrase at the next boot.

This project is intentionally small. It uses Go, `udevadm`, systemd, and a root-owned configuration file. It does not depend on a desktop environment.

## Install on Arch Linux

Install the package-building tools:

```sh
sudo pacman -Syu --needed git go base-devel systemd
```

Build and install the package as an ordinary user:

```sh
git clone https://github.com/sesjaphp/usbkill.git
cd usbkill
makepkg -si
```

The `PKGBUILD` is deliberately local-source only. After the checkout is present, `makepkg` does not clone GitHub, fetch a remote source, or require a GitHub login. You may also copy the complete project directory to an offline Arch machine and run `makepkg -si` there.

The package installs `/usr/bin/usbkill`, `/usr/lib/systemd/system/usbkill.service`, and `/etc/usbkill`. Installation never configures, enables, or arms the watchdog automatically.

## Configure and test

List USB storage devices:

```sh
sudo usbkill list
```

Select a device and save its VID, PID, and unique serial:

```sh
sudo usbkill setup
```

Test detection without powering off:

```sh
sudo usbkill test
```

Remove the token during test mode. The program must report the removal and remain running. Reconnect the token, then arm it:

```sh
sudo usbkill arm
sudo systemctl enable --now usbkill.service
sudo usbkill status
```

To disable the trigger:

```sh
sudo usbkill disarm
sudo systemctl disable --now usbkill.service
```

View logs with:

```sh
journalctl -u usbkill.service -f
```

## Recovery

If the token is lost or the service repeatedly powers off the computer, boot an Arch ISO, mount the installed root filesystem at `/mnt`, and disable the service:

```sh
mount /dev/ROOT_PARTITION /mnt
arch-chroot /mnt systemctl disable usbkill.service
rm -f /mnt/run/usbkill/armed
rm -f /mnt/etc/usbkill/config.yaml
```

The exact root partition and any encrypted-volume setup depend on the machine. Do not arm the service again until `usbkill test` has been completed successfully.

## Configuration and security

The configuration is stored at `/etc/usbkill/config.yaml` and contains the USB vendor ID, product ID, serial, and bounded timeouts. It is written with mode `0600`. The program refuses devices without a serial number and refuses to arm if zero or multiple devices match.

When armed and a matching udev removal event arrives, the daemon enters a one-shot shutdown path. It waits only for a bounded best-effort cleanup window and then calls `systemctl poweroff` with fixed arguments. It never invokes a shell with user input. It does not claim guaranteed RAM erasure: normal userspace cannot guarantee clearing all DRAM, kernel memory, CPU caches, GPU memory, DMA buffers, firmware state, or swap. Encrypted swap is strongly preferable, and this project never initiates hibernation.

This is defense in depth, not a complete physical-security system. An attacker may remove storage, boot external media, change firmware, disable the service, disconnect power, tamper with hardware, or compromise the operating system.

## Development checks

```sh
make fmt
make vet
make test
go build .
```

Arch package checks, when run on Arch Linux, are:

```sh
makepkg --printsrcinfo > .SRCINFO
makepkg -f
```

The package build reads the Go source, service unit, README, and license from the same local directory as `PKGBUILD`.
