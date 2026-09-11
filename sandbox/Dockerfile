# =============================================================================
# Qraft Sandbox — Isolated Code Execution via nsjail
# =============================================================================
# Provides a secure, sandboxed environment for running untrusted user code
# with strict resource limits enforced by nsjail.
# =============================================================================

FROM golang:1.23.12-bookworm@sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db AS go-toolchain

FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90 AS builder

ARG NSJAIL_COMMIT=d6454b4640b6d8699b532a8afa37e4d67e477078

ENV DEBIAN_FRONTEND=noninteractive

# Install build dependencies for nsjail
RUN apt-get update && apt-get install -y --no-install-recommends \
    autoconf \
    bison \
    ca-certificates \
    flex \
    g++ \
    gcc \
    git \
    libnl-3-dev \
    libnl-route-3-dev \
    libprotobuf-dev \
    libseccomp-dev \
    libtool \
    make \
    pkg-config \
    protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

# Build a pinned nsjail revision, including the Kafel submodule selected by it.
RUN git init /nsjail \
    && git -C /nsjail remote add origin https://github.com/google/nsjail.git \
    && git -C /nsjail fetch --depth 1 origin "${NSJAIL_COMMIT}" \
    && git -C /nsjail checkout --detach FETCH_HEAD \
    && git -C /nsjail submodule update --init --depth 1 \
    && cd /nsjail \
    && make -j$(nproc) \
    && mv /nsjail/nsjail /usr/local/bin/nsjail

# =============================================================================
# Runtime Stage
# =============================================================================
FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH=/usr/local/go/bin:${PATH}

# Install runtime dependencies and language compilers
RUN apt-get update && apt-get install -y --no-install-recommends \
    # nsjail runtime deps
    libnl-3-200 \
    libnl-route-3-200 \
    libprotobuf32t64 \
    libseccomp2 \
    # C/C++ compiler
    g++ \
    gcc \
    # Boost headers are part of the standard competitive-programming
    # toolchain (notably multiprecision/cpp_int).  Keep them in the sandbox so
    # generated programs and contestant solutions see the same baseline.
    libboost-dev \
    libc6-dev \
    # Python
    python3 \
    # Java
    default-jdk-headless \
    # Utilities
    ca-certificates \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Go is copied from an immutable language-version stage instead of relying on
# the Ubuntu package, whose 24.04 baseline is older than the locked Go 1.23.
COPY --from=go-toolchain /usr/local/go /usr/local/go

# Refuse to build an image whose language baselines drift from the supported language baselines.
RUN python3 --version | grep -E '^Python 3\.12\.' \
    && javac -version 2>&1 | grep -E '^javac 21\.' \
    && go version | grep -E '^go version go1\.23\.' \
    && printf '#include <stdio.h>\nint main(void){return 0;}\n' | gcc -x c -std=c17 -fsyntax-only - \
    && printf '#include <concepts>\nint main(){return 0;}\n' | g++ -x c++ -std=c++20 -fsyntax-only - \
    && printf '#include <boost/multiprecision/cpp_int.hpp>\nint main(){boost::multiprecision::cpp_int x=1; return x==1?0:1;}\n' | g++ -x c++ -std=c++20 -fsyntax-only -

# Copy the pinned executable and its upstream licenses.
COPY --from=builder /usr/local/bin/nsjail /usr/local/bin/nsjail
COPY --from=builder /nsjail/LICENSE /usr/share/licenses/nsjail/LICENSE
COPY --from=builder /nsjail/kafel/LICENSE /usr/share/licenses/kafel/LICENSE

# Per-request roots and workspaces are created by the service. The service
# runs as root only so nsjail can create namespaces; submitted code is always
# mapped to uid/gid 65534 inside the jail.
RUN mkdir -p /sandbox/app /sandbox/audit \
    && chmod 700 /sandbox/audit

# Copy sandbox service application
COPY . /sandbox/app/

WORKDIR /sandbox/app

# Record the exact resolved package set. The base image is immutable, but apt
# repositories are not; this manifest makes any rebuild drift observable and
# is included in every compile/execute audit record by digest.
RUN dpkg-query -W -f='${binary:Package}=${Version}\n' | LC_ALL=C sort > /sandbox/app/toolchain-packages.txt

ARG SANDBOX_REVISION=unset
ENV SANDBOX_REVISION=${SANDBOX_REVISION}

LABEL org.opencontainers.image.revision="${SANDBOX_REVISION}"

# Bind the runtime revision claim to image content. Runtime overrides that do
# not match this baked value are rejected before the audit sink or HTTP server
# is opened.
RUN printf '%s\n' "${SANDBOX_REVISION}" > /sandbox/app/source-revision.txt \
    && chmod 0444 /sandbox/app/source-revision.txt

# Build the sandbox service if it has a Go module
RUN if [ -f go.mod ]; then \
        go build -o /sandbox/app/sandbox-server . ; \
    fi

EXPOSE 8090

# Run as root because nsjail requires privileges for namespace creation
# The executed user code runs as an unprivileged user inside the jail
ENTRYPOINT ["/sandbox/app/sandbox-server"]
CMD ["serve"]
