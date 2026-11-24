package android

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// maxRecordSeconds is the longest that screenrecord records: Android stops it at three minutes.
const maxRecordSeconds = 180

// RecOptions say how StartRecording records.
type RecOptions struct {
	// TimeLimit is how long the recording may run at most. Zero is 180 seconds, which is also the most
	// that Android allows.
	TimeLimit time.Duration
	// Size is WIDTHxHEIGHT, or "" for the size of the screen.
	Size string
	// BitRate is in bits per second, or 0 for the default of Android.
	BitRate int
	// Output is the absolute path of the .mp4 file that StopRecording writes. Its folder must exist.
	Output string
}

var recSizeExpr = regexp.MustCompile(`^[1-9][0-9]{1,4}x[1-9][0-9]{1,4}$`)

// recording is a recording that is going on.
type recording struct {
	out string
	// device is the file on the device, and child is the screenrecord that writes it. Both are empty for a
	// server at serverUrl, which keeps the recording itself.
	device string
	child  Child
}

func (o RecOptions) check() (seconds int, err error) {
	switch {
	case o.TimeLimit < 0:
		return 0, errors.New("the time limit is negative")
	case o.TimeLimit == 0:
		seconds = maxRecordSeconds
	case o.TimeLimit > maxRecordSeconds*time.Second:
		return 0, fmt.Errorf("a recording lasts %d seconds at most: Android stops screenrecord there", maxRecordSeconds)
	default:
		seconds = int((o.TimeLimit + time.Second - 1) / time.Second)
	}
	if o.Size != "" && !recSizeExpr.MatchString(o.Size) {
		return 0, errors.New("the size is WIDTHxHEIGHT, such as 720x1280")
	}
	if o.BitRate != 0 && (o.BitRate < 100_000 || o.BitRate > 200_000_000) {
		return 0, errors.New("the bit rate is between 100000 and 200000000")
	}
	if !filepath.IsAbs(o.Output) || !strings.EqualFold(filepath.Ext(o.Output), ".mp4") {
		return 0, errors.New("the output is the absolute path of an .mp4 file")
	}
	if info, err := os.Stat(filepath.Dir(o.Output)); err != nil || !info.IsDir() {
		return 0, errors.New("the folder of the output does not exist")
	}
	if info, err := os.Lstat(o.Output); err == nil && !info.Mode().IsRegular() {
		return 0, errors.New("the output is not a regular file")
	}
	return seconds, nil
}

// StartRecording starts recording the screen to a file on the device (screenrecord), or at the server
// (serverUrl). One recording runs at a time. StopRecording pulls the file to Output, and Platform.Close
// ends a recording that is still going and deletes its file on the device.
func (c *Controller) StartRecording(ctx context.Context, o RecOptions) error {
	seconds, err := o.check()
	if err != nil {
		return err
	}
	c.mu.Lock()
	busy := c.rec != nil
	c.mu.Unlock()
	if busy {
		return errors.New("a recording is already running: stop it first")
	}
	if c.Remote() {
		u, err := c.ui(ctx)
		if err != nil {
			return err
		}
		opts := map[string]any{"timeLimit": seconds}
		if o.Size != "" {
			opts["videoSize"] = o.Size
		}
		if o.BitRate != 0 {
			opts["bitRate"] = o.BitRate
		}
		if err := u.call(ctx, http.MethodPost, "/appium/start_recording_screen", map[string]any{"options": opts}, nil, false); err != nil {
			return err
		}
		c.mu.Lock()
		c.rec = &recording{out: o.Output}
		c.mu.Unlock()
		return nil
	}
	a, err := c.adb()
	if err != nil {
		return err
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("a file name could not be made: %w", err)
	}
	device := "/data/local/tmp/semar-rec-" + hex.EncodeToString(rnd[:]) + ".mp4"
	words := []string{"screenrecord", "--time-limit", fmt.Sprint(seconds)}
	if o.Size != "" {
		words = append(words, "--size", o.Size)
	}
	if o.BitRate != 0 {
		words = append(words, "--bit-rate", fmt.Sprint(o.BitRate))
	}
	words = append(words, device)
	child, err := a.run.Start(ctx, a.bin, a.args("shell", shellCommand(words...))...)
	if err != nil {
		return fmt.Errorf("the recording could not be started: %w", err)
	}
	// A screenrecord that ends at once did not start: a device with no encoder, or a size it refuses.
	t := time.NewTimer(300 * time.Millisecond)
	defer t.Stop()
	select {
	case <-child.Done():
		reason := lastLine(child.Tail())
		if reason == "" {
			reason = "it ended at once"
		}
		return fmt.Errorf("the recording could not be started: %s", reason)
	case <-t.C:
	case <-ctx.Done():
		child.Kill()
		return ctx.Err()
	}
	c.mu.Lock()
	c.rec = &recording{out: o.Output, device: device, child: child}
	c.mu.Unlock()
	return nil
}

// stopWait is how long screenrecord is given to finish the file after it was told to stop.
const stopWait = 5 * time.Second

// StopRecording stops the recording, writes it to the Output of StartRecording, and gives that path.
// screenrecord is stopped with SIGINT, which lets it close the file: killing the adb client would leave a
// file that does not play. The file on the device is deleted whether or not it could be pulled.
func (c *Controller) StopRecording(ctx context.Context) (string, error) {
	c.mu.Lock()
	rec := c.rec
	c.rec = nil
	c.mu.Unlock()
	if rec == nil {
		return "", errors.New("no recording is running")
	}
	if rec.child == nil {
		return c.stopRemote(ctx, rec)
	}
	a, err := c.adb()
	if err != nil {
		return "", err
	}
	// An error of pkill is no sign of a failure: the recording may have reached its time limit.
	_, _ = a.shell(ctx, "pkill", "-INT", "screenrecord")
	t := time.NewTimer(stopWait)
	select {
	case <-rec.child.Done():
		t.Stop()
	case <-t.C:
		rec.child.Kill()
	case <-ctx.Done():
		t.Stop()
		rec.child.Kill()
	}
	_, pullErr := a.out(ctx, "pull", rec.device, rec.out)
	// The pulled file takes the mode that the umask gives it: the video may show what the person's app
	// shows, so it is the person's alone, whatever the folder.
	var chmodErr error
	if pullErr == nil {
		chmodErr = os.Chmod(rec.out, 0o600)
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, rmErr := a.shell(rctx, "rm", "-f", rec.device)
	switch {
	case pullErr != nil:
		return "", fmt.Errorf("the recording could not be pulled: %w", pullErr)
	case chmodErr != nil:
		return rec.out, fmt.Errorf("the recording was saved, but its permissions could not be set to 0600: %w", errors.Unwrap(chmodErr))
	case rmErr != nil:
		return rec.out, fmt.Errorf("the recording was saved, but its file on the device could not be deleted: %w", rmErr)
	}
	return rec.out, nil
}

// maxRecordingAnswer bounds the video that a server gives, as base64.
const maxRecordingAnswer = 96 << 20

func (c *Controller) stopRemote(ctx context.Context, rec *recording) (string, error) {
	u, err := c.ui(ctx)
	if err != nil {
		return "", err
	}
	var b64 string
	if err := u.callMax(ctx, maxRecordingAnswer, http.MethodPost, "/appium/stop_recording_screen", map[string]any{}, &b64, false); err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(data) == 0 {
		return "", errors.New("the server gave no video")
	}
	if err := os.WriteFile(rec.out, data, 0o600); err != nil {
		return "", fmt.Errorf("the recording could not be written: %w", errors.Unwrap(err))
	}
	// WriteFile keeps the mode of a file that was there already.
	if err := os.Chmod(rec.out, 0o600); err != nil {
		return rec.out, fmt.Errorf("the recording was saved, but its permissions could not be set to 0600: %w", errors.Unwrap(err))
	}
	return rec.out, nil
}

// abortRecording ends a recording without keeping it, and deletes its file on the device. The caller
// holds c.mu.
func (c *Controller) abortRecording(ctx context.Context, a *adb) error {
	rec := c.rec
	c.rec = nil
	if rec == nil || rec.child == nil {
		return nil
	}
	_, _ = a.shell(ctx, "pkill", "-INT", "screenrecord")
	t := time.NewTimer(stopWait)
	defer t.Stop()
	select {
	case <-rec.child.Done():
	case <-t.C:
		rec.child.Kill()
	case <-ctx.Done():
		rec.child.Kill()
	}
	if _, err := a.shell(context.WithoutCancel(ctx), "rm", "-f", rec.device); err != nil {
		return err
	}
	return nil
}

// undoRemote ends a recording at serverUrl, which the server keeps and would otherwise let run. The
// caller holds c.mu.
func (c *Controller) undoRemote(ctx context.Context) []error {
	if c.rec == nil || c.p.ctlSess == nil || c.p.client == nil {
		c.rec = nil
		return nil
	}
	c.rec = nil
	sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	u := uiConn{c.p.ctlSess, c.p.client}
	if err := u.callMax(sctx, maxRecordingAnswer, http.MethodPost, "/appium/stop_recording_screen", map[string]any{}, nil, false); err != nil {
		return []error{fmt.Errorf("the screen recording could not be stopped: %w", err)}
	}
	return nil
}
