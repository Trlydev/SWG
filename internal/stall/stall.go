// Package stall fetches files without a limit on the whole download, only on
// silence: a request is given up when no data arrives for a while.
//
// A limit on the whole request (http.Client.Timeout) cut off big downloads
// on slow lines however steadily they progressed (seen: 01 STUDIO mods
// failing at exactly 5 minutes, "context deadline exceeded ... while
// reading body"), while a connection that hangs is still caught here.
package stall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// ErrStalled is returned when a download sent nothing for the idle time.
var ErrStalled = errors.New("the download stalled")

// Do sends req with c, minus c's overall Timeout, and gives it up when the
// response (headers, then each part of the body) takes longer than idle to
// come. The body must be closed.
func Do(c *http.Client, req *http.Request, idle time.Duration) (*http.Response, error) {
	cc := *c
	cc.Timeout = 0
	ctx, cancel := context.WithCancel(req.Context())
	var fired atomic.Bool
	t := time.AfterFunc(idle, func() { fired.Store(true); cancel() })
	resp, err := cc.Do(req.WithContext(ctx))
	if err != nil {
		t.Stop()
		cancel()
		if fired.Load() {
			return nil, fmt.Errorf("%w: no answer for %s", ErrStalled, idle)
		}
		return nil, err
	}
	resp.Body = &body{ReadCloser: resp.Body, t: t, idle: idle, cancel: cancel, fired: &fired}
	return resp, nil
}

type body struct {
	io.ReadCloser
	t      *time.Timer
	idle   time.Duration
	cancel context.CancelFunc
	fired  *atomic.Bool
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && !b.fired.Load() {
		b.t.Reset(b.idle)
	}
	if err != nil && err != io.EOF && b.fired.Load() {
		return n, fmt.Errorf("%w: no data for %s", ErrStalled, b.idle)
	}
	return n, err
}

func (b *body) Close() error {
	b.t.Stop()
	b.cancel()
	return b.ReadCloser.Close()
}
