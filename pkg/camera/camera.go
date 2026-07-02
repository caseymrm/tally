// Package camera reports whether any camera on this Mac is in use by any
// application, using CoreMediaIO's DeviceIsRunningSomewhere property. This
// covers Zoom, Google Meet, FaceTime, and anything else that opens a
// capture device, without needing camera permission or log parsing.
package camera

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreMediaIO -framework CoreFoundation
#include "camera.h"
*/
import "C"

import (
	"context"
	"sync"
	"time"
)

const (
	// pollInterval is a safety net in case a CMIO notification is missed.
	pollInterval = 5 * time.Second
	// offDelay debounces brief off blips (e.g. an app re-opening the
	// device) so the light doesn't flicker.
	offDelay = 3 * time.Second
)

var (
	listenOnce sync.Once
	poke       = make(chan struct{}, 1)
)

//export goCameraChanged
func goCameraChanged() {
	select {
	case poke <- struct{}{}:
	default:
	}
}

// InUse reports whether any camera is currently in use by any process.
func InUse() bool {
	return bool(C.wizcameraAnyCameraOn())
}

// Watch reports the camera-in-use state: the current state immediately,
// then a value on every change until ctx is done. Changes are driven by
// CoreMediaIO notifications with polling as a fallback. Off transitions are
// held for a few seconds to absorb blips. Only one Watch should be active
// per process.
func Watch(ctx context.Context) <-chan bool {
	ch := make(chan bool, 1)
	listenOnce.Do(func() {
		C.wizcameraStartListeners()
	})
	go func() {
		defer close(ch)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		last := InUse()
		ch <- last
		for {
			select {
			case <-ctx.Done():
				return
			case <-poke:
			case <-ticker.C:
			}
			current := InUse()
			if current == last {
				continue
			}
			if !current {
				select {
				case <-ctx.Done():
					return
				case <-time.After(offDelay):
				}
				current = InUse()
				if current == last {
					continue
				}
			}
			last = current
			select {
			case <-ctx.Done():
				return
			case ch <- current:
			}
		}
	}()
	return ch
}
