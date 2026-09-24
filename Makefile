.PHONY: setup up down logs test analyze test-backend

setup:
	@test -f .env || python3 -c 'import os,pathlib,secrets; os.umask(0o077); s=pathlib.Path(".env.example").read_text().replace("replace-with-random-token", secrets.token_hex(32)).replace("replace-with-64-hex-characters", secrets.token_hex(32)); open(".env","x").write(s)'
	@echo "Environment ready in .env"

up: setup
	docker compose up --build -d --wait

down:
	docker compose down

logs:
	docker compose logs -f

test:
	cd backend && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./...
	cd runner && cargo test
	cd web && flutter test

analyze:
	cd web && flutter analyze
	cd backend && go vet ./...
	cd runner && cargo fmt --check

# Uses the local Compose PostgreSQL database; override for another test database.
TEST_DATABASE_URL ?= postgres://n9n:n9n_local_dev@127.0.0.1:55432/n9n?sslmode=disable
test-backend:
	docker compose up -d --wait postgres
	cd backend && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race ./...
