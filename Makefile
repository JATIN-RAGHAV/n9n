.PHONY: setup up down logs test analyze

setup:
	@test -f .env || python3 -c 'import os,pathlib,secrets; os.umask(0o077); s=pathlib.Path(".env.example").read_text().replace("replace-with-random-token", secrets.token_hex(32)).replace("replace-with-64-hex-characters", secrets.token_hex(32)); open(".env","x").write(s)'
	@echo "Environment ready in .env"

up: setup
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f

test:
	cd backend && go test ./...
	cd runner && cargo test
	cd web && flutter test

analyze:
	cd web && flutter analyze
	cd backend && go vet ./...
	cd runner && cargo fmt --check
