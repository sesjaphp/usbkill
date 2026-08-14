.PHONY: all build test fmt vet install package check-service-hardening clean

all: build

build:
	go build -trimpath -o usbkill .

test:
	go test .

fmt:
	gofmt -w main.go main_test.go

vet:
	go vet .

install: build
	install -Dm0755 usbkill /usr/bin/usbkill
	install -Dm0644 usbkill.service /usr/lib/systemd/system/usbkill.service
	install -dm0750 /etc/usbkill
	systemctl daemon-reload

package:
	makepkg -f

check-service-hardening:
	systemd-analyze verify usbkill.service
	@if systemctl is-system-running >/dev/null 2>&1; then \
		if output=$$(systemd-run --quiet --wait --pipe --property=NoNewPrivileges=yes /usr/bin/grep -q '^NoNewPrivs:[[:space:]]*1' /proc/self/status 2>&1); then \
			echo 'NoNewPrivileges runtime probe passed'; \
		else \
			echo "NoNewPrivileges runtime probe unavailable in this environment: $$output"; \
		fi; \
	else \
		echo 'NoNewPrivileges runtime probe skipped: systemd is not the active init'; \
	fi

clean:
	rm -f usbkill usbkill-*.pkg.tar.*
