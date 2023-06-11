
all: build

build:  mtq_controller mtq_lock_server mtq_operator

generate:
	chmod 777 ./vendor/k8s.io/code-generator/generate-groups.sh
	./hack/update-codegen.sh

mtq_controller:
	go build -o mtq_controller -v cmd/mtq-controller/*.go
	chmod 777 mtq_controller

mtq_operator:
	go build -o mtq_operator -v cmd/mtq-operator/*.go
	chmod 777 mtq_operator

mtq_lock_server:
	go build -o mtq_lock_server -v cmd/mtq-lock-server/*.go
	chmod 777 mtq_lock_server

clean:
	rm ./mtq_controller ./mtq_operator ./mtq_lock_server -f

dist-clean: clean
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_controller' -a -q`
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_lock_server' -a -q`
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_operator' -a -q`

fmt:
	go fmt .

run: build
	sudo ./mtq_controller

build-images: build-mtq-lock-server-image build-mtq-controller-image build-mtq-operator-image

build-mtq-lock-server-image:
	docker build -t quay.io/bmordeha/kubevirt/mtq_lock_server -f Dockerfile.lockServer  .
	docker push  quay.io/bmordeha/kubevirt/mtq_lock_server

build-mtq-controller-image:
	docker build -t quay.io/bmordeha/kubevirt/mtq_controller -f Dockerfile.controller .
	docker push  quay.io/bmordeha/kubevirt/mtq_controller

build-mtq-operator-image:
	docker build -t quay.io/bmordeha/kubevirt/mtq_operator -f Dockerfile.operator .
	docker push  quay.io/bmordeha/kubevirt/mtq_operator

.PHONY: clean test fmt sync docker
