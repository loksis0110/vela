//go:build windows

package main

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPeekMessageW       = user32.NewProc("PeekMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
)

const (
	wmHotkey      = 0x0312
	wmUser        = 0x0400
	wmApply       = 0x8000 + 1 // WM_APP + 1: apply a new combination
	modNoRepeat   = 0x4000
	hotkeyID      = 0x5645 // "VE"
	pmNoRemove    = 0x0000
	errHotkeyUsed = 1409 // ERROR_HOTKEY_ALREADY_REGISTERED
)

type winMsg struct {
	Hwnd     windows.HWND
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       struct{ X, Y int32 }
	LPrivate uint32
}

type hotkeyReq struct {
	spec   *hotkeySpec
	result chan error
}

// RegisterHotKey delivers WM_HOTKEY to the thread that registered it, so
// the hotkey lives on its own locked OS thread with a message loop.
var hotkeyThread struct {
	once  sync.Once
	tid   uint32
	ready chan struct{}
	reqs  chan hotkeyReq
}

// registerHotkey replaces the global hotkey (nil turns it off). onPress
// runs in a new goroutine for each press.
func registerHotkey(spec *hotkeySpec, onPress func()) error {
	h := &hotkeyThread
	h.once.Do(func() {
		h.ready = make(chan struct{})
		h.reqs = make(chan hotkeyReq, 4)
		go hotkeyLoop(onPress)
	})
	<-h.ready
	if h.tid == 0 {
		return errors.New("hotkey thread failed")
	}
	req := hotkeyReq{spec: spec, result: make(chan error, 1)}
	h.reqs <- req
	procPostThreadMessageW.Call(uintptr(h.tid), wmApply, 0, 0)
	select {
	case err := <-req.result:
		return err
	case <-time.After(3 * time.Second):
		return errors.New("hotkey thread does not answer")
	}
}

func hotkeyLoop(onPress func()) {
	runtime.LockOSThread()
	h := &hotkeyThread
	var msg winMsg
	// makes sure the thread has a message queue before anyone posts to it
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, wmUser, wmUser, pmNoRemove)
	h.tid = windows.GetCurrentThreadId()
	close(h.ready)
	registered := false
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		switch msg.Message {
		case wmHotkey:
			if msg.WParam == hotkeyID {
				go onPress()
			}
		case wmApply:
			for pending := true; pending; {
				select {
				case req := <-h.reqs:
					if registered {
						procUnregisterHotKey.Call(0, hotkeyID)
						registered = false
					}
					var err error
					if req.spec != nil {
						ok, _, e := procRegisterHotKey.Call(0, hotkeyID, uintptr(req.spec.Mods|modNoRepeat), uintptr(req.spec.VK))
						if ok != 0 {
							registered = true
						} else if errno, isErrno := e.(syscall.Errno); isErrno && errno == errHotkeyUsed {
							err = fmt.Errorf(tr("сочетание %s уже занято другой программой"), req.spec.Text)
						} else {
							err = fmt.Errorf(tr("не удалось назначить %s: %v"), req.spec.Text, e)
						}
					}
					req.result <- err
				default:
					pending = false
				}
			}
		}
	}
}
