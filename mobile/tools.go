//go:build tools

package velacore

// Keeps the gomobile runtime and the sing-box mobile library in go.mod/go.sum,
// so `go mod tidy` records their dependencies before `gomobile bind`.
import (
	_ "github.com/sagernet/gomobile"
	_ "github.com/sagernet/sing-box/experimental/libbox"
)
