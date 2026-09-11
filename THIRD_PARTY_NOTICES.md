# Third-party notices

Qraft · 题构 builds on open-source software. The root [LICENSE](LICENSE) covers this project's own code; third-party components retain their respective licenses and copyright notices.

## Desktop and interface

The Windows client includes go-webview2 (MIT), go-winloader (MIT), golang.org/x/sys (BSD-3-Clause), the Microsoft WebView2Loader SDK (BSD-3-Clause), and the Go standard library (BSD-3-Clause). Exact notices and Microsoft WebView2 Runtime terms are described in [desktop/THIRD_PARTY_NOTICES.md](desktop/THIRD_PARTY_NOTICES.md). The documented vendor patch remains in [desktop/VENDOR_PATCHES.md](desktop/VENDOR_PATCHES.md).

React, Next.js, Vite, Monaco Editor, KaTeX, Lucide, React Flow, and other interface dependencies are recorded in [frontend/package-lock.json](frontend/package-lock.json). Desktop packaging collects available runtime dependency license and notice files into `licenses/npm`. Keep those files when redistributing the binaries.

## Execution sandbox

| Component | License | Pinned source |
| --- | --- | --- |
| nsjail | Apache-2.0 | [d6454b4640b6d8699b532a8afa37e4d67e477078](https://github.com/google/nsjail/tree/d6454b4640b6d8699b532a8afa37e4d67e477078) · [LICENSE](https://github.com/google/nsjail/blob/d6454b4640b6d8699b532a8afa37e4d67e477078/LICENSE) |
| Kafel | Apache-2.0 | [76d0f41bf3eb5c4008713d64b9767b461a9129a3](https://github.com/google/kafel/tree/76d0f41bf3eb5c4008713d64b9767b461a9129a3) · [LICENSE](https://github.com/google/kafel/blob/76d0f41bf3eb5c4008713d64b9767b461a9129a3/LICENSE) |

The sandbox Dockerfile builds nsjail and its pinned Kafel submodule from these sources. Compilers, language runtimes, system libraries, and base images have separate licenses; their package notices must remain with the redistributed image.

## Backend services

Go dependency versions are recorded in [backend/go.mod](backend/go.mod), [backend/go.sum](backend/go.sum), and [sandbox/go.mod](sandbox/go.mod). Self-hosting also runs independent third-party services:

| Service | License information |
| --- | --- |
| PostgreSQL and pgvector | [PostgreSQL license](https://www.postgresql.org/about/licence/) · [pgvector source and license](https://github.com/pgvector/pgvector) |
| Redis | [Redis source and licensing](https://github.com/redis/redis); consult the license of the exact pinned image version |
| MinIO | [MinIO source and AGPL-3.0 license](https://github.com/minio/minio/blob/master/LICENSE) |
| Temporal | [Temporal server](https://github.com/temporalio/temporal) · [Temporal UI](https://github.com/temporalio/ui) |
| Caddy | [Caddy source and Apache-2.0 license](https://github.com/caddyserver/caddy/blob/master/LICENSE) |

[docker-compose.yml](docker-compose.yml) records the image references. These services and their contained packages are not relicensed by Qraft's root license. Preserve their license files and any applicable source-distribution obligations when distributing container images.
