FROM golang:1.26-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /n9n-backend .

FROM alpine:3.22
RUN addgroup -S n9n && adduser -S -G n9n n9n && mkdir /data && chown n9n:n9n /data
COPY --from=build /n9n-backend /usr/local/bin/n9n-backend
USER n9n
EXPOSE 8080
CMD ["/usr/local/bin/n9n-backend"]
