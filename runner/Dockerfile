FROM rust:1.96-alpine AS build
RUN apk add --no-cache musl-dev
WORKDIR /src
COPY runner/Cargo.toml runner/Cargo.lock ./
COPY runner/src ./src
RUN cargo build --release --locked

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S n9n && adduser -S -G n9n n9n
COPY --from=build /src/target/release/n9n-runner /usr/local/bin/n9n-runner
USER n9n
CMD ["/usr/local/bin/n9n-runner"]
