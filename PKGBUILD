pkgname=usbkill
pkgver=0.1.0
pkgrel=1
pkgdesc='Simple Arch Linux USB-presence shutdown watchdog'
arch=('x86_64')
url='https://github.com/sesjaphp/usbkill'
license=('MIT')
depends=('systemd')
makedepends=('go')
source=('usbkill::git+https://github.com/sesjaphp/usbkill.git#branch=main')
sha256sums=('SKIP')

build() {
  cd "$srcdir/usbkill"
  go build -trimpath -ldflags="-s -w" -o usbkill .
}

check() {
  cd "$srcdir/usbkill"
  go test ./...
}

package() {
  cd "$srcdir/usbkill"
  install -Dm0755 usbkill "$pkgdir/usr/bin/usbkill"
  install -Dm0644 usbkill.service "$pkgdir/usr/lib/systemd/system/usbkill.service"
  install -Dm0644 README.md "$pkgdir/usr/share/doc/usbkill/README.md"
  install -dm0750 "$pkgdir/etc/usbkill"
}
