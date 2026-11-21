BINARY  := renstra
CMD     := ./cmd/renstra
VERSION ?= v1.2.6
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test vet clean linux-amd64 linux-arm64

all: test vet build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) $(CMD)

test:
	go test -count=1 ./...

vet:
	go vet ./...

linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY)-linux-amd64 $(CMD)

linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY)-linux-arm64 $(CMD)

clean:
	rm -f $(BINARY) $(BINARY).exe $(BINARY)-linux-amd64 $(BINARY)-linux-arm64
