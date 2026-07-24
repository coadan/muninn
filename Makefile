.PHONY: test build install

BINARY := muninn
DIST := dist/$(BINARY)

test:
	go test ./...

build:
	mkdir -p dist
	go build -o $(DIST) .

install: test build
	@target="$$(go env GOPATH)/bin/$(BINARY)"; \
	mkdir -p "$$(dirname "$$target")"; \
	tmp="$${target}.tmp.$$$$"; \
	cp "$(DIST)" "$$tmp"; \
	chmod +x "$$tmp"; \
	mv "$$tmp" "$$target"; \
	echo "installed $$target"
