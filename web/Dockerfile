FROM ghcr.io/cirruslabs/flutter:3.35.7 AS build
WORKDIR /app
COPY web/pubspec.yaml web/pubspec.lock web/analysis_options.yaml ./
RUN flutter pub get
COPY web/ .
RUN flutter build web --wasm --release

FROM nginx:1.27-alpine
COPY web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/build/web /usr/share/nginx/html
EXPOSE 80
