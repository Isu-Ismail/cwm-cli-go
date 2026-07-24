# Dockerfile
# Custom builder image with Go and GoReleaser.
# Allows compiling and packaging Windows ZIP, Linux DEB/RPM, and macOS tar.gz on any system running Docker.
FROM ubuntu:22.04

# Prevent interactive prompts
ENV DEBIAN_FRONTEND=noninteractive

# Install core build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    unzip \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Go 1.22.5
RUN curl -sSL https://go.dev/dl/go1.22.5.linux-amd64.tar.gz | tar -xz -C /usr/local
ENV PATH=$PATH:/usr/local/go/bin

# Install GoReleaser v1.26.2 (pre-compiled binary)
RUN curl -sSL https://github.com/goreleaser/goreleaser/releases/download/v1.26.2/goreleaser_Linux_x86_64.tar.gz | tar -xz -C /usr/bin goreleaser

WORKDIR /workdir

# Default build command
CMD ["goreleaser", "release", "--clean", "--skip=publish", "--skip=validate"]
