.PHONY: test race vet build
 test:
	go test ./...
race:
	go test -race ./...
vet:
	go vet ./...
build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/rcodex-go ./cmd/rcg
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/rcgctl ./cmd/rcgctl
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/rcodex-relay ./cmd/rcg-relay
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/rcg-ops ./cmd/rcg-ops
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/rcg-admin ./cmd/rcg-admin
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/rcg-loadtest ./cmd/rcg-loadtest
