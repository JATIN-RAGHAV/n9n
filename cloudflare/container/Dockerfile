# syntax=docker/dockerfile:1
# Build context is the repository root (see image_build_context in wrangler.jsonc).
# Both compile stages run on the build machine's native architecture and
# cross-compile for the target (Cloudflare Containers run linux/amd64).

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend-build
ARG TARGETARCH
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /n9n-backend .

FROM --platform=$BUILDPLATFORM rust:1.96-bookworm AS runner-build
ARG TARGETARCH
WORKDIR /src
RUN set -eux; \
    case "$TARGETARCH" in \
      amd64) target=x86_64-unknown-linux-gnu; gcc=x86_64-linux-gnu-gcc; pkgs="gcc-x86-64-linux-gnu libc6-dev-amd64-cross" ;; \
      arm64) target=aarch64-unknown-linux-gnu; gcc=aarch64-linux-gnu-gcc; pkgs="gcc-aarch64-linux-gnu libc6-dev-arm64-cross" ;; \
      *) echo "unsupported TARGETARCH=$TARGETARCH" >&2; exit 1 ;; \
    esac; \
    rustup target add "$target"; \
    if ! command -v "$gcc" >/dev/null; then \
      apt-get update && apt-get install -y --no-install-recommends $pkgs && rm -rf /var/lib/apt/lists/*; \
    fi; \
    upper=$(echo "$target" | tr 'a-z-' 'A-Z_'); lower=$(echo "$target" | tr '-' '_'); \
    { echo "export RUST_TARGET=$target"; \
      echo "export CARGO_TARGET_${upper}_LINKER=$gcc"; \
      echo "export CC_${lower}=$gcc"; } > /etc/rust-target.env
COPY runner/Cargo.toml runner/Cargo.lock ./
COPY runner/src ./src
RUN . /etc/rust-target.env && cargo build --release --locked --target "$RUST_TARGET" \
    && cp "target/$RUST_TARGET/release/n9n-runner" /n9n-runner

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -r n9n && useradd -r -g n9n -d /nonexistent -s /usr/sbin/nologin n9n \
    && mkdir -p /tmp/n9n-health && chown n9n:n9n /tmp/n9n-health
COPY --from=backend-build /n9n-backend /usr/local/bin/n9n-backend
COPY --from=runner-build /n9n-runner /usr/local/bin/n9n-runner
COPY --chmod=755 cloudflare/container/entrypoint.sh /usr/local/bin/n9n-entrypoint
USER n9n
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/n9n-entrypoint"]
