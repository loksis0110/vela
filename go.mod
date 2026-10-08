module github.com/velaapp/vela

go 1.25.5

require (
	fyne.io/systray v1.12.2
	github.com/sagernet/sing v0.9.6-0.20260922013354-87c33f17688f
	github.com/sagernet/gomobile v0.1.13
	github.com/sagernet/sing-box v1.14.2
	golang.org/x/sys v0.47.0
)

// Patched core, created by scripts/prepare-core.sh (run automatically by make and CI).
replace github.com/sagernet/sing-box => ./third_party/sing-box
