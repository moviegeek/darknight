# darknight — moviegeek media library
.PHONY: all backend frontend dev build test vet fmt clean

# Dev defaults. Override on the command line, e.g. `make dev DARKNIGHT_DB=/tmp/x.db`.
DARKNIGHT_DB ?= .data/darknight.db
DARKNIGHT_ADDR ?= :8080

all: backend frontend

# ---------- backend (Go) ----------

backend:
	go build -o bin/darknight ./cmd/darknight

dev: backend
	DARKNIGHT_DB=$(DARKNIGHT_DB) DARKNIGHT_ADDR=$(DARKNIGHT_ADDR) ./bin/darknight

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

# ---------- frontend (web/) ----------

frontend:
	cd web && npm install && npm run build

frontend-dev:
	cd web && npm run dev

# ---------- full build (embeds frontend into binary) ----------

build: frontend backend
	@echo "built bin/darknight (frontend embedded via web/dist)"

clean:
	rm -rf bin/ web/dist web/node_modules
