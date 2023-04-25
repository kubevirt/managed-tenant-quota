
all: build

build:  mtq_controller

generate:
	./hack/update-codegen.sh

mtq_controller:
	go build -o mtq_controller -v cmd/mtq-controller/*.go
	chmod 777 mtq_controller

clean:
	rm ./mtq_controller -f

dist-clean: clean
	docker rmi -f `docker images 'quay.io/bmordeha/kubevirt/mtq_controller' -a -q`

fmt:
	go fmt .

run: build
	sudo ./mtq_controller

build-image:
	docker build -t quay.io/bmordeha/kubevirt/mtq_controller .
	docker push  quay.io/bmordeha/kubevirt/mtq_controller


.PHONY: clean test fmt sync docker
