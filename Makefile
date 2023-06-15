#Copyright 2023 The MTQ Authors.
#
#Licensed under the Apache License, Version 2.0 (the "License");
#you may not use this file except in compliance with the License.
#You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
#Unless required by applicable law or agreed to in writing, software
#distributed under the License is distributed on an "AS IS" BASIS,
#WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#See the License for the specific language governing permissions and
#limitations under the License.

.PHONY: manifests \
		cluster-up cluster-down cluster-sync \
		test test-functional test-unit test-lint \
		publish \
		vet \
		format \
		goveralls \
		release-description \
		bazel-generate bazel-build bazel-build-images bazel-push-images \
		fossa
all: build

build:  mtq_controller mtq_lock_server mtq_operator

DOCKER?=1
ifeq (${DOCKER}, 1)
	# use entrypoint.sh (default) as your entrypoint into the container
	DO=./hack/build/in-docker.sh
	# use entrypoint-bazel.sh as your entrypoint into the container.
	DO_BAZ=./hack/build/bazel-docker.sh
else
	DO=eval
	DO_BAZ=eval
endif

all: manifests bazel-build-images

manifests:
	${DO_BAZ} "DOCKER_PREFIX=${DOCKER_PREFIX} DOCKER_TAG=${DOCKER_TAG} VERBOSITY=${VERBOSITY} PULL_POLICY=${PULL_POLICY} CR_NAME=${CR_NAME} MTQ_NAMESPACE=${CDI_NAMESPACE} ./hack/build/build-manifests.sh"

builder-push:
	./hack/build/bazel-build-builder.sh


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

