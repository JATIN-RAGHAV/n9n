FROM golang:1.26-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /n9n-backend .

FROM alpine:3.22
RUN addgroup -S n9n && adduser -S -G n9n n9n
COPY --from=build /n9n-backend /usr/local/bin/n9n-backend
USER n9n
EXPOSE 8080
CMD ["/usr/local/bin/n9n-backend"]
