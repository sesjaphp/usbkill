.PHONY: all build test fmt vet install package clean

all: build

build:
	go build -trimpath -o usbkill .

test:
	go test .

fmt:
	gofmt -w main.go

vet:
	go vet .

install: build
	install -Dm0755 usbkill /usr/bin/usbkill
	install -Dm0644 usbkill.service /usr/lib/systemd/system/usbkill.service
	install -dm0750 /etc/usbkill
	systemctl daemon-reload

package:
	makepkg -f

clean:
	rm -f usbkill usbkill-*.pkg.tar.*
