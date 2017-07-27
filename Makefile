GO=CGO_ENABLED=0 go
BIN=gitlab-bot

DOCKER_IMAGE=quay.io/smeriwether/gitlab-slack-notifier
DOCKER_TAG=latest

run:
	go run main.go

install:
	dep ensure

test: install
	go test $$(go list ./... | grep -v /vendor/)

build: install
	$(GO) build -a -installsuffix cgo -o $(BIN) .

docker: docker_build docker_push

docker_build: build
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

docker_push:
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
