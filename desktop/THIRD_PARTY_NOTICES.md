# Third-party notices

Qraft uses the following independent open-source components. Their licenses apply separately from the project's root license; upstream names and copyright notices are retained.

| Component | License | Source / notice |
| --- | --- | --- |
| go-webview2 | MIT | `vendor/github.com/jchv/go-webview2/LICENSE` |
| go-winloader | MIT | `vendor/github.com/jchv/go-winloader/LICENSE.md` |
| golang.org/x/sys | BSD-3-Clause | `vendor/golang.org/x/sys/LICENSE` and `PATENTS` |
| Microsoft WebView2Loader SDK | BSD-3-Clause | `vendor/github.com/jchv/go-webview2/webviewloader/sdk/LICENSE.txt` |
| Go standard library | BSD-3-Clause | `assets/Go-LICENSE` |

The exact Go versions are recorded in `go.mod` and `vendor/modules.txt`. The small go-webview2 permission change is documented in [VENDOR_PATCHES.md](VENDOR_PATCHES.md).

The bundled interface uses React, Lucide, Monaco Editor, KaTeX and its fonts, React Flow, Zustand, and their runtime dependencies. Exact versions and license metadata are recorded in `../frontend/package-lock.json`. Packaging copies available license and notice files from those installed packages into `licenses/npm`.

Microsoft Edge WebView2 Runtime and its optional bootstrapper are separate Microsoft components, distributed under [Microsoft's WebView2 terms](https://developer.microsoft.com/microsoft-edge/webview2/). The bootstrapper is unmodified and its Authenticode signature is checked before packaging. The loader SDK notice above does not replace the Runtime's terms.

Windows installers are built with [NSIS](https://nsis.sourceforge.io/License). Installer and portable packages include these third-party notices and the collected license files in `licenses/`. When redistributing a standalone executable, accompany it with those same notices.

Self-hosted backend services and container images have their own licenses. Consult the repository's root third-party notices and the notices provided with each image; this desktop notice does not relicense them.
