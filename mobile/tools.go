//go:build tools

package velacore

// Keeps the gomobile bind runtime in go.mod so `gomobile bind` works
// after `go mod tidy`.
import _ "github.com/sagernet/gomobile"
