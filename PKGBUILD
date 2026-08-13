pkgname=usbkill
pkgver=0.2.0
pkgrel=1
pkgdesc='Simple Arch Linux USB-presence shutdown watchdog'
arch=('x86_64')
url='https://github.com/sesjaphp/usbkill'
license=('MIT')
depends=('systemd')
makedepends=('go')
source=()

build() {
  cd "$startdir"
  go build -trimpath -ldflags="-s -w" -o usbkill .
}

check() {
  cd "$startdir"
  go test .
}

package() {
  cd "$startdir"
  install -Dm0755 usbkill "$pkgdir/usr/bin/usbkill"
  install -Dm0644 usbkill.service "$pkgdir/usr/lib/systemd/system/usbkill.service"
  install -Dm0644 README.md "$pkgdir/usr/share/doc/usbkill/README.md"
  install -Dm0644 LICENSE "$pkgdir/usr/share/licenses/usbkill/LICENSE"
  install -dm0750 "$pkgdir/etc/usbkill"
}
