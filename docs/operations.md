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
| `sudo usbkill status` | Shows token matching state, live service state, current arming-marker state, and boot auto-arm state. | None |

The internal `daemon` and `boot-autoarm` commands are executed by systemd. Do not use them as routine operator commands. Direct `systemctl` control and status operations have a 45-second deadline. Each `udevadm info` lookup has a 5-second deadline, so device discovery and control commands return an error rather than waiting indefinitely if a helper becomes stuck. Boot auto-arm uses one 25-second total budget shared by its discovery and activation steps, within the unit's 30-second start deadline.

## Normal setup and verification

Begin with the token connected. Configure it once, then verify test mode before any real arming.

```sh
sudo usbkill list
sudo usbkill setup
sudo usbkill test
```

While test mode is running, remove the configured token. The expected final log is `TEST MODE: poweroff suppressed`, and the command must exit without an error. If test mode instead reports a `TEST MODE: udev monitor stream ...; poweroff suppressed` message, no poweroff occurred, but the monitor test failed and production arming must not proceed. Reconnect the token, then inspect the state:

```sh
sudo usbkill status
```

When you are ready to enable the real watchdog, save work first and run:

```sh
sudo usbkill arm
sudo systemctl is-active usbkill.service
sudo journalctl -u usbkill.service -b -n 30 --no-pager
```

A successful production arming check reports `active`. The readiness-confirmed start has a 30-second deadline; arming fails if the daemon cannot report readiness within that bound. The enclosing direct `systemctl` operation has a 45-second deadline. After readiness, systemd requires a watchdog heartbeat every 30 seconds and the daemon sends one every 10 seconds. If the armed daemon stops sending heartbeats, `FailureAction=poweroff` requests a normal system poweroff. This policy is intentionally destructive on a liveness failure; verify it only after saving work and completing the non-destructive removal test. Do not remove the token unless you intend to request a real poweroff.

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
| Boot auto-arm's 25-second total budget expires | Boot unit records the discovery or activation failure. | Disarmed |
| Auto-arm operation exceeds 30 seconds | Boot unit times out and records the failure. | Disarmed |

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
| `Service: ACTIVE` | Run `sudo usbkill status`. | The monitor is currently running. `Armed: yes` alone records marker state, not live process health. |
| `Armed: no` after reboot | Run `sudo usbkill status`. | Normal unless boot auto-arm is enabled and the token was present. |
| `usbkill.service` is inactive while disarmed | `sudo systemctl status usbkill.service`. | Expected; the unit is gated by the transient armed marker. |
| Boot auto-arm did not arm | `sudo journalctl -u usbkill-autoarm.service -b --no-pager`. | Check token presence, ambiguity, discovery errors, the 25-second total boot budget, or the outer 30-second start timeout. |
| A `usbkill` command reports a helper timeout | Check `journalctl -b`, `systemctl status systemd-udevd.service`, and the affected service state. | `systemctl` operations are bounded at 45 seconds; each identity lookup is bounded at 5 seconds. Investigate the host service rather than repeatedly waiting. |
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

## Service hardening verification

The watchdog and boot auto-arm units use `NoNewPrivileges=yes`, empty capability bounding sets, strict filesystem controls, namespace restrictions, restricted socket families, and write-execute memory protection. Static validation cannot prove that these settings preserve a real systemd poweroff transaction on every Arch installation.

After installing a hardening update, test in this order. Keep the configured token connected until the final controlled removal check.

```sh
sudo usbkill disarm
sudo systemctl daemon-reload
sudo systemctl show usbkill.service -p NoNewPrivileges -p CapabilityBoundingSet -p ProtectSystem -p RestrictNamespaces -p MemoryDenyWriteExecute
sudo usbkill test
```

The test-mode removal must still log `TEST MODE: poweroff suppressed`. Reconnect the token, save all work, arm the production service, and verify it has reached readiness:

```sh
sudo usbkill arm
sudo systemctl is-active usbkill.service
sudo journalctl -u usbkill.service -b -n 30 --no-pager
```

The final validation is intentionally destructive: remove the token only when a real poweroff is safe. After rebooting, inspect the previous boot's journal for a successful shutdown request or an explicit failure:

```sh
sudo journalctl -u usbkill.service -b -1 --no-pager
sudo journalctl -u usbkill-failure.service -b -1 --no-pager
```

If the service fails to start, remains inactive, or the controlled removal does not request poweroff, immediately disarm it, disable boot auto-arm if enabled, and restore the last known-good package revision before further investigation.
