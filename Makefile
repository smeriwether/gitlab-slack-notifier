GO=CGO_ENABLED=0 go
BIN=gitlab-bot

run:
	go run main.go

install:
	dep ensure

build: install
	$(GO) build -a -installsuffix cgo -o $(BIN) .

test: install
	go test $$(go list ./... | grep -v /vendor/)
