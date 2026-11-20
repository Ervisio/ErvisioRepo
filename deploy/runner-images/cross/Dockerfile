# Cross-compilation image for the highpriority-cross runner label: the act
# Ubuntu 24.04 image plus toolchains that build Windows and macOS binaries
# on Linux. Built on the runner machine, never pushed.
FROM ghcr.io/catthehacker/ubuntu:act-24.04

LABEL dev.ervisio.runner.keep="true" \
      org.opencontainers.image.description="Ervisio runner: Linux to Windows/macOS cross toolchains"

ARG ZIG_VERSION=0.14.1
ENV DEBIAN_FRONTEND=noninteractive

RUN dpkg --add-architecture i386 \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      mingw-w64 mingw-w64-tools gcc-mingw-w64 g++-mingw-w64 \
      clang lld llvm cmake ninja-build pkg-config \
      nsis osslsigncode p7zip-full zip xz-utils \
      wine wine64 wine32:i386 \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/zig-x86_64-linux-${ZIG_VERSION}.tar.xz" -o /tmp/zig.tar.xz \
 && mkdir -p /opt/zig && tar -xJf /tmp/zig.tar.xz -C /opt/zig --strip-components=1 \
 && ln -s /opt/zig/zig /usr/local/bin/zig && rm /tmp/zig.tar.xz \
 && zig version

ENV WINEDEBUG=-all
