# Security policy

## Scope

Security reports include unexpected poweroff behavior, ways to bypass exact USB identity matching, unsafe test-mode behavior, shell or command injection, configuration permission bypasses, service-hardening regressions, and packaging or startup changes that weaken the documented threat model.

`usbkill` is defense in depth for a running Arch Linux workstation. It does not guarantee RAM, CPU-cache, GPU-memory, firmware-memory, DMA, cold-boot, kernel, root-attacker, firmware, early-boot, or physical-tamper protection.

## Reporting a vulnerability

Please do not open a public GitHub issue for an unpatched vulnerability. Use GitHub's private vulnerability reporting feature on the repository when available. If that feature is unavailable, contact the repository maintainer privately through the GitHub profile for `sesjaphp` and include `[security]` in the subject.

Include a concise description, affected commit or release, reproduction steps that do not power off a real machine, expected behavior, actual behavior, and a proposed mitigation if known. Remove USB serial numbers, hostnames, usernames, IP addresses, disk identifiers, credentials, and other private data from reports.

The maintainer will acknowledge a report when practical, investigate it, and coordinate disclosure after a fix is available. Please do not publish exploit details before coordination.

## Safe disclosure requirements

Never submit a proof of concept that invokes `systemctl poweroff` on a developer machine. Use test mode, mocks, a disposable VM, or a fake command boundary. Security fixes should include a regression test and an update to the threat-model documentation.
