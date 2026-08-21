.PHONY: test bench vet build clean

test:
	go test -race ./...

bench:
	go test -bench=. -benchmem ./algorithm/

vet:
	go vet ./...

build:
	go build ./...

clean:
	go clean ./...
