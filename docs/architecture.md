# Architecture overview

`usbkill` is intentionally a single small Go program with no network listener, database, custom kernel component, or external Go dependency. Arch Linux provides `udevadm` for device identity and event monitoring and systemd provides the service and power-management boundary.

## Runtime flow

The CLI has separate setup, status, test, arm, disarm, boot auto-arm control, and daemon paths. Setup enumerates physical USB storage devices, skips partitions, requires a stable serial number, and atomically writes a root-owned configuration. Test mode loads the configuration and uses an injected mock poweroff implementation. Arm verifies exactly one matching token, creates the transient armed marker, and enables the production service. The explicit `enable-autoarm` command persists an opt-in marker and enables a boot unit; on later boots it arms only when exactly one configured token is present. Boot auto-arm has one 25-second total context budget shared by discovery and service activation, inside the unit's 30-second startup deadline. Disarm stops and disables the production service before removing the marker.

The production daemon requires the armed marker, acquires an exclusive monitor lock, starts the udev property monitor, and only then performs the final exact-token verification. This ordering keeps removal events queued while identity verification runs. It notifies systemd of readiness only after that monitor process is running and verification has succeeded, so `arm` does not report success before the daemon has entered its monitoring path. A matching removal is authoritative. The daemon schedules the configured pre-poweroff delay and invokes a fixed-argument, bounded `systemctl --no-ask-password --ignore-inhibitors poweroff` command. Failed attempts are logged and retried within a fixed bound. If all attempts fail, the daemon returns failure to systemd without an automatic service restart, then invokes `usbkill-failure.service` to record an `authpriv.alert` journal entry. The runtime directory preserves the armed marker across a service restart and is removed on explicit stop and reboot.

## Safety boundaries

The `Poweroff` interface is the test seam: tests use `MockPoweroff`, while production constructs `RealPoweroff`. The event matcher requires `ACTION=remove`, `ID_BUS=usb`, exact vendor and product IDs, and a non-empty canonical serial. Udev records have an explicit 256 KiB maximum; malformed oversized records fail the production stream closed and remain non-destructive in test mode. Configuration and state files must be regular, owned by the executing root process, and not writable by group or other users. The monitor lock prevents test and production daemons from running concurrently.

Startup behavior is deliberately asymmetric. Test mode refuses an absent or ambiguous token without powering off. An armed production service treats an absent or ambiguous token as a fail-closed condition and enters the bounded shutdown path. Reconnects do not cancel a shutdown already in progress.

## Contribution guidance

Changes affecting identity matching, startup, boot auto-arm, poweroff, service hardening, configuration permissions, or package installation require focused regression tests and documentation updates. Automated tests must never invoke the real poweroff command. See the [operations guide](operations.md), [CONTRIBUTING.md](../CONTRIBUTING.md), and [SECURITY.md](../SECURITY.md).
