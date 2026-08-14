# Contributing to usbkill

Thank you for helping improve `usbkill`. It is a small security-sensitive Arch Linux daemon, so contributions should favor clarity, narrow scope, explicit failure handling, and tests over feature breadth.

## Before you start

Read the [README](README.md), [security model](docs/security.md), and [SECURITY.md](SECURITY.md). Do not include real USB serial numbers, private system logs, disk identifiers, credentials, or other device-identifying information in issues or pull requests.

For security vulnerabilities, do not open a public issue. Follow the private reporting instructions in `SECURITY.md`.

## Local development

Install the normal Arch development tools:

```sh
sudo pacman -Syu --needed base-devel go systemd git
```

Clone the public repository and create a branch:

```sh
git clone https://github.com/sesjaphp/usbkill.git
cd usbkill
git switch -c my-change
```

## Safe testing rules

Never test a change against a production installation with the real systemd service enabled. Automated tests must never execute `systemctl poweroff`. Use the injected mock poweroff implementation and `go test` for logic tests. Physical USB testing must use a disposable test machine or a controlled environment, must begin in `usbkill test` mode, and must keep the production service disabled until the test has passed.

Do not run `sudo makepkg`. Run `makepkg` as the normal user; pacman requests privilege only when installing the package. Do not use a disk required to boot the test machine as the USB token.

## Required checks

Run the following before opening a pull request:

```sh
gofmt -w main.go main_test.go
go test .
go test ./...
go test -race .
go vet .
go build -trimpath -o usbkill .
bash -n PKGBUILD
systemd-analyze verify usbkill.service
git diff --check
```

For package changes, also run `makepkg --printsrcinfo > .SRCINFO` and `makepkg -f` on Arch Linux. Explain if an Arch-native check could not be run.

## Change guidelines

Keep the standard-library implementation small and auditable. Do not add networking, custom drivers, initramfs integration, cryptography, memory-wiping tricks, or shell command construction. Poweroff commands must use fixed arguments and bounded contexts. Changes to identity matching, startup behavior, shutdown, service hardening, or package installation require regression tests and documentation updates.

Use focused commits with imperative subjects. Keep unrelated formatting or refactoring out of feature changes. Pull requests should explain the threat-model impact, test coverage, recovery implications, and any remaining limitations.

## Pull requests

Open a pull request against `main` using the repository template. A maintainer will review security-sensitive changes carefully. Passing CI is required, but CI cannot replace a human review of changes affecting poweroff behavior.
