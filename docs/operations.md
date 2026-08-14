# Operations guide

`usbkill` is an Arch Linux physical-presence watchdog for an already-running, already-unlocked workstation. It is not a disk-encryption key, a lock-screen replacement, or a memory-sanitization tool. Treat every production arming or removal check as potentially destructive.

## Runtime state

| State | Location | Lifetime | Meaning |
|---|---|---|---|
| Configuration | `/etc/usbkill/config.yaml` | Persistent | Exact VID, PID, serial, delay, and timeout settings. |
| Boot auto-arm opt-in | `/etc/usbkill/auto-arm` | Persistent | Enables the boot auto-arm unit. |
| Armed marker | `/run/usbkill/armed` | Current boot | Allows the production watchdog to start. |
| Monitor lock | `/run/usbkill/monitor.lock` | Current process | Prevents test and production monitors from running together. |

The `/run` state is cleared at reboot. The persistent auto-arm setting does not by itself mean the watchdog is armed; it allows the boot unit to arm only after it confirms exactly one configured token.

## Command reference

| Command | Effect | Destructive risk |
|---|---|---|
| `sudo usbkill list` | Shows USB storage devices with valid VID, PID, and non-empty serial identities. | None |
| `sudo usbkill setup` | Selects one listed token and writes the root-owned configuration. | None |
| `sudo usbkill test` | Runs a removable-token test with poweroff replaced by a mock. | None; poweroff is suppressed. |
| `sudo usbkill arm` | Verifies one configured token, writes the armed marker, and enables and starts the watchdog. | Removing the token afterward requests real poweroff. |
| `sudo usbkill disarm` | Stops and disables the watchdog and removes the armed marker. | None |
| `sudo usbkill enable-autoarm` | Persists the boot auto-arm opt-in and enables the boot unit. | Does not arm the current session. |
| `sudo usbkill disable-autoarm` | Disables future boot auto-arming and removes the opt-in marker. | Does not disarm the current session. |
| `sudo usbkill status` | Shows token matching state, current arming state, and boot auto-arm state. | None |

The internal `daemon` and `boot-autoarm` commands are executed by systemd. Do not use them as routine operator commands.

## Normal setup and verification

Begin with the token connected. Configure it once, then verify test mode before any real arming.

```sh
sudo usbkill list
sudo usbkill setup
sudo usbkill test
```

While test mode is running, remove the configured token. The expected final log is `TEST MODE: poweroff suppressed`. Reconnect the token, then inspect the state:

```sh
sudo usbkill status
```

When you are ready to enable the real watchdog, save work first and run:

```sh
sudo usbkill arm
sudo systemctl is-active usbkill.service
sudo journalctl -u usbkill.service -b -n 30 --no-pager
```

A successful production arming check reports `active`. Do not remove the token unless you intend to request a real poweroff.

## Boot auto-arm decision table

The boot auto-arm feature is intentionally opt-in. Enable it only after test mode and a manual arming check have both succeeded.

```sh
sudo usbkill enable-autoarm
sudo usbkill status
```

| Boot condition | Auto-arm result | Watchdog state |
|---|---|---|
| Opt-in marker absent | Boot unit is skipped. | Disarmed |
| Opt-in marker present; exactly one token match | Boot unit arms the watchdog. | Armed and monitoring |
| Opt-in marker present; no token match | Boot unit logs the condition and exits successfully. | Disarmed |
| Opt-in marker present; multiple token matches | Boot unit logs the condition and exits successfully. | Disarmed |
| Opt-in marker present; discovery error | Boot unit fails and records the error. | Disarmed |

To test boot auto-arm, save work, leave the token connected, reboot, and then inspect:

```sh
sudo reboot
sudo usbkill status
sudo systemctl is-active usbkill.service
sudo journalctl -u usbkill-autoarm.service -b --no-pager
```

Disable future boot auto-arm with `sudo usbkill disable-autoarm`. To disable future boot auto-arm and stop current monitoring, run both `sudo usbkill disable-autoarm` and `sudo usbkill disarm`.

## Diagnostics

| Symptom | First checks | Expected interpretation |
|---|---|---|
| `Watchdog: ABSENT` | Reconnect the configured token, then run `sudo usbkill list`. | The configured identity is not currently visible. |
| `Watchdog: AMBIGUOUS` | Disconnect duplicate or cloned matching devices. | Do not arm until exactly one identity matches. |
| `Armed: no` after reboot | Run `sudo usbkill status`. | Normal unless boot auto-arm is enabled and the token was present. |
| `usbkill.service` is inactive while disarmed | `sudo systemctl status usbkill.service`. | Expected; the unit is gated by the transient armed marker. |
| Boot auto-arm did not arm | `sudo journalctl -u usbkill-autoarm.service -b --no-pager`. | Check token presence, ambiguity, or discovery errors. |
| Shutdown was requested but did not occur | `sudo journalctl -u usbkill.service -b --no-pager` and `sudo journalctl -u usbkill-failure.service -b --no-pager`. | The daemon logs up to three bounded poweroff attempts and records terminal failure. |

## Upgrade procedure

Disarm before replacing the package. This avoids updating a running watchdog.

```sh
cd ~/usbkill
sudo usbkill disarm
sudo chown -R "$USER:$USER" .
rm -rf pkg src
git pull --ff-only origin main
makepkg -si
sudo systemctl daemon-reload
```

After an upgrade, begin with `sudo usbkill test`. If boot auto-arm is already enabled, confirm its state with `sudo usbkill status` before rebooting.

## Recovery

If the token is lost or a configuration error repeatedly triggers shutdown, boot Arch installation media, mount the installed root filesystem, and remove both persistent configuration and boot auto-arm state:

```sh
mount /dev/ROOT_PARTITION /mnt
arch-chroot /mnt systemctl disable usbkill.service usbkill-autoarm.service
rm -f /mnt/run/usbkill/armed
rm -f /mnt/etc/usbkill/auto-arm
rm -f /mnt/etc/usbkill/config.yaml
```

Do not re-enable the watchdog until the token and configuration have completed a non-destructive test.
