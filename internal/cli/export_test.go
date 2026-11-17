package cli

import (
	"context"
	"io"
)

type Options = options

func RunWith(ctx context.Context, args []string, stdout, stderr io.Writer, o Options) int {
	return run(ctx, args, stdout, stderr, o)
}

// WithRecording makes semar record run with no window, and lets drive send the input of the person
// once the window is open. The window is closed when drive returns, as the person does.
func WithRecording(ctx context.Context, drive func(tab context.Context)) context.Context {
	return withRecording(ctx, recordingHooks{hidden: true, drive: drive})
}

// WithTrustedScreens makes a run treat the screens of an app as approved, without storing an approval.
func WithTrustedScreens(ctx context.Context, app string) context.Context {
	return withTrustedScreens(ctx, app)
}
