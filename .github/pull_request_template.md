## Summary

Describe the change and why it is needed.

## Security and threat model

- [ ] Does this change affect USB identity matching, startup, arming, shutdown, service hardening, configuration, or packaging?
- [ ] Have I explained the security impact and remaining limitations?
- [ ] Does the change preserve the documented threat model?

## Safety

- [ ] Automated tests cannot execute `systemctl poweroff`.
- [ ] I did not include real USB serial numbers, private logs, credentials, or host details.
- [ ] I considered recovery behavior and service restart behavior.

## Validation

- [ ] `gofmt -w main.go main_test.go`
- [ ] `go test .`
- [ ] `go test -race .`
- [ ] `go vet .`
- [ ] `go build -trimpath -o usbkill .`
- [ ] `bash -n PKGBUILD` (when applicable)
- [ ] `systemd-analyze verify usbkill.service usbkill-failure.service` (when applicable)
- [ ] `makepkg --printsrcinfo > .SRCINFO` and `makepkg -f` on Arch (when applicable)

## Documentation

- [ ] README or security documentation is updated when behavior changes.
- [ ] Tests were added or updated for changed behavior.
- [ ] Any checks unavailable in my environment are explained below.

## Notes

Describe test environment, limitations, migration steps, or follow-up work.
