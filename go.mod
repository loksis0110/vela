module github.com/velaapp/vela

go 1.26

require (
	github.com/makiuchi-d/gozxing v0.1.1
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	fyne.io/systray v1.12.2
	github.com/sagernet/sing v0.9.6-0.20260922013354-87c33f17688f
	github.com/sagernet/gomobile v0.1.13
	github.com/sagernet/sing-box v1.14.2
	golang.org/x/sys v0.47.0
	github.com/xtls/xray-core v1.260327.1-0.20260728075948-5ca6f4b7d4dc
	github.com/miekg/dns v1.1.72
	google.golang.org/grpc v1.82.1
	gopkg.in/yaml.v3 v3.0.1
)

// Patched core, created by scripts/prepare-core.sh (run automatically by make and CI).
replace github.com/sagernet/sing-box => ./third_party/sing-box
