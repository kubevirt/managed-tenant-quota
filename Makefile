
all: build

build:  mtq_controller mtq_webhook mtq_operator

generate:
	chmod 777 ./vendor/k8s.io/code-generator/generate-groups.sh
	./hack/update-codegen.sh

mtq_controller:
	go build -o mtq_controller -v cmd/mtq-controller/*.go
	chmod 777 mtq_controller

mtq_operator:
	go build -o mtq_operator -v cmd/mtq-operator/*.go
	chmod 777 mtq_operator

mtq_webhook:
	go build -o mtq_webhook -v cmd/mtq-webhook/*.go
	chmod 777 mtq_webhook

clean:
	rm ./mtq_controller ./mtq_operator ./mtq_webhook -f

dist-clean: clean
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_controller' -a -q`
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_webhook' -a -q`
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_operator' -a -q`

fmt:
	go fmt .

run: build
	sudo ./mtq_controller

build-images: build-mtq-webhook-image build-mtq-controller-image build-mtq-operator-image

build-mtq-webhook-image:
	docker build -t quay.io/bmordeha/kubevirt/mtq_webhook -f Dockerfile.webhook  .
	docker push  quay.io/bmordeha/kubevirt/mtq_webhook

build-mtq-controller-image:
	docker build -t quay.io/bmordeha/kubevirt/mtq_controller -f Dockerfile.controller .
	docker push  quay.io/bmordeha/kubevirt/mtq_controller

build-mtq-operator-image:
	docker build -t quay.io/bmordeha/kubevirt/mtq_operator -f Dockerfile.operator .
	docker push  quay.io/bmordeha/kubevirt/mtq_operator

.PHONY: clean test fmt sync docker
