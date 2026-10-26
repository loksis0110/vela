# WebView2 host

Adapted from [github.com/jchv/go-webview2](https://github.com/jchv/go-webview2)
(commit 56598839c808, MIT, see LICENSE) for the Vela window on Windows:

- `edge`: the WebView2 COM bindings; failures to create the browser are
  returned in `Chromium.Err` instead of exiting the process, the package no
  longer locks the main thread in `init`, and `Chromium.Close` was added;
- `webviewloader`: always loads the WebView2Loader.dll embedded in Vela.exe
  (never a DLL from disk next to the exe);
- `w32`: Win32 helpers, unchanged.

The window itself is in `../window_windows.go`.
