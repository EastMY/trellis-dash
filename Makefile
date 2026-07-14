.PHONY: dev backend frontend test build clean

dev:
	go run ./cmd/trellis-dashboard serve --database .data/dashboard.db

backend:
	go build ./cmd/trellis-dashboard

frontend:
	cd frontend && pnpm build
	rm -rf internal/webui/dist
	cp -R frontend/dist internal/webui/dist

test:
	go test ./...
	cd frontend && pnpm test -- --run

build: frontend
	mkdir -p bin
	go build -trimpath -ldflags "-s -w" -o bin/trellis-dashboard ./cmd/trellis-dashboard

clean:
	rm -rf bin frontend/dist internal/webui/dist
	mkdir -p internal/webui/dist
	touch internal/webui/dist/index.html
