.PHONY: build run test docker-build docker-push deploy

IMG ?= ghcr.io/soloz-io/spoke-identity-operator:latest

build:
	go build -o bin/manager ./cmd/

run: build
	./bin/manager

test:
	go test ./...

docker-build:
	docker build -t ${IMG} .

docker-push:
	docker push ${IMG}

deploy:
	kubectl apply -f config/rbac/role.yaml
	kubectl apply -f config/manager/manager.yaml
