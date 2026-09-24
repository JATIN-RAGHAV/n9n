.PHONY: setup up down logs test

setup:
	@test -f .env || (cp .env.example .env && python3 -c 'import pathlib,secrets; p=pathlib.Path(".env"); s=p.read_text().replace("replace-with-random-token", secrets.token_hex(32)).replace("replace-with-64-hex-characters", secrets.token_hex(32)); p.write_text(s)')
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
