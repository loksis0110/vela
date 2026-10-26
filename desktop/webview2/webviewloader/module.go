//go:build windows

// Package webviewloader loads Microsoft's WebView2Loader from the copy
// embedded in Vela.exe (never from disk: a WebView2Loader.dll placed next to
// the exe, e.g. in Downloads, must not be loaded) and exposes the functions
// Vela needs.
//
// Based on github.com/jchv/go-webview2/webviewloader (MIT, see ../LICENSE).
// WebView2Loader.dll is redistributed under Microsoft's WebView2 SDK license
// (sdk/LICENSE.txt).
package webviewloader

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/jchv/go-winloader"
	"golang.org/x/sys/windows"
)

var (
	memOnce                                         sync.Once
	memModule                                       winloader.Module
	memCreate                                       winloader.Proc
	memGetAvailableCoreWebView2BrowserVersionString winloader.Proc
	memErr                                          error
)

func load() error {
	memOnce.Do(func() {
		memModule, memErr = winloader.LoadFromMemory(WebView2Loader)
		if memErr != nil {
			memErr = fmt.Errorf("unable to load the embedded WebView2Loader.dll: %w", memErr)
			return
		}
		memCreate = memModule.Proc("CreateCoreWebView2EnvironmentWithOptions")
		memGetAvailableCoreWebView2BrowserVersionString = memModule.Proc("GetAvailableCoreWebView2BrowserVersionString")
		if memCreate == nil || memGetAvailableCoreWebView2BrowserVersionString == nil {
			memErr = fmt.Errorf("the embedded WebView2Loader.dll misses functions")
		}
	})
	return memErr
}

// GetInstalledVersion returns the installed version of the WebView2 runtime,
// or "" when it is not installed.
func GetInstalledVersion() (string, error) {
	if err := load(); err != nil {
		return "", err
	}
	var result *uint16
	hr64, _, _ := memGetAvailableCoreWebView2BrowserVersionString.Call(
		uint64(uintptr(unsafe.Pointer(nil))),
		uint64(uintptr(unsafe.Pointer(&result))))
	hr := uint32(hr64)
	defer windows.CoTaskMemFree(unsafe.Pointer(result)) // safe with nil
	if hr != uint32(windows.S_OK) {
		if hr&0xFFFF == uint32(windows.ERROR_FILE_NOT_FOUND) {
			return "", nil // not installed
		}
		return "", fmt.Errorf("GetAvailableCoreWebView2BrowserVersionString returned HRESULT 0x%X", hr)
	}
	return windows.UTF16PtrToString(result), nil
}

// CreateCoreWebView2EnvironmentWithOptions calls the loader function of the
// same name.
func CreateCoreWebView2EnvironmentWithOptions(browserExecutableFolder, userDataFolder *uint16, environmentOptions uintptr, environmentCompletedHandle uintptr) (uintptr, error) {
	if err := load(); err != nil {
		return 0, err
	}
	res, _, _ := memCreate.Call(
		uint64(uintptr(unsafe.Pointer(browserExecutableFolder))),
		uint64(uintptr(unsafe.Pointer(userDataFolder))),
		uint64(environmentOptions),
		uint64(environmentCompletedHandle),
	)
	return uintptr(res), nil
}
