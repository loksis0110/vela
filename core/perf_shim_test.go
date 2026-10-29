package core

import (
	"context"

	box "github.com/sagernet/sing-box"
)

// perfAppRepick is what the desktop watchdog does after a failed check.
func perfAppRepick(ctx context.Context, instance *box.Box) { RepickAuto(ctx, instance) }
