.PHONY: help check format-check test vet build install

BINARY := muninn
DIST := dist/$(BINARY)

help:
	@echo "Muninn development targets:"
	@echo "  check    format-check, test, and vet"
	@echo "  test     run Go tests"
	@echo "  build    build dist/muninn"
	@echo "  install  check, build, and install into GOPATH/bin"

check: format-check test vet

format-check:
	@unformatted="$$(gofmt -l $$(find cmd internal -type f -name '*.go'))"; \
	if [ -n "$$unformatted" ]; then \
		echo "Go files require gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

test:
	go test ./...

vet:
	go vet ./...

build:
	mkdir -p dist
	go build -o $(DIST) ./cmd/muninn

install: check build
	@target="$$(go env GOPATH)/bin/$(BINARY)"; \
	mkdir -p "$$(dirname "$$target")"; \
	tmp="$${target}.tmp.$$$$"; \
	cp "$(DIST)" "$$tmp"; \
	chmod +x "$$tmp"; \
	mv "$$tmp" "$$target"; \
	echo "installed $$target"
