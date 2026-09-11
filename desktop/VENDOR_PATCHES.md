# Desktop dependency patch

`github.com/jchv/go-webview2` is pinned to `56598839c808` and vendored with its license. One line in `webview.go` changes ClipboardRead permission from automatic Allow to Default. Remote service pages must use normal browser permission handling. The local manager and remote workspace use separate processes and profiles; only the embedded manager receives native bindings.

After refreshing vendor, reapply this line and run the Windows build and native smoke test. Do not publish an unpatched build. No other upstream source is changed.
