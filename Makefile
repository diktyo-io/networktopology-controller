# Copyright 2020 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ARCHS = amd64 arm64
COMMONENVVAR=GOOS=$(shell uname -s | tr A-Z a-z)
BUILDENVVAR=CGO_ENABLED=0
INTEGTESTENVVAR=NETWORKTOPOLOGY_CONTROLLER_TEST_VERBOSE=1
LOCAL_REGISTRY=localhost:5000/network-topology-controller
LOCAL_CONTROLLER_IMAGE=controller:latest

.PHONY: all
all: build

.PHONY: build
build: build-controller

.PHONY: build.amd64
build.amd64: build-controller.amd64

.PHONY: build.arm64v8
build.arm64v8: build-controller.arm64v8

.PHONY: build-controller
build-controller:
	$(COMMONENVVAR) $(BUILDENVVAR) go build -ldflags '-w' -o bin/controller cmd/controller/controller.go

.PHONY: build-controller.amd64
build-controller.amd64:
	$(COMMONENVVAR) $(BUILDENVVAR) GOARCH=amd64 go build -ldflags '-w' -o bin/controller cmd/controller/controller.go

.PHONY: build-controller.arm64v8
build-controller.arm64v8:
	GOOS=linux $(BUILDENVVAR) GOARCH=arm64 go build -ldflags '-w' -o bin/controller cmd/controller/controller.go

.PHONY: local-image
local-image: clean
	docker build -f ./build/controller/Dockerfile --build-arg ARCH="amd64" -t $(LOCAL_REGISTRY)/$(LOCAL_CONTROLLER_IMAGE) .

.PHONY: local-image.amd64
local-image.amd64: clean
	docker build -f ./build/controller/Dockerfile --build-arg ARCH="amd64" -t $(LOCAL_REGISTRY)/$(LOCAL_CONTROLLER_IMAGE) .

.PHONY: local-image.arm64v8
local-image.arm64v8: clean
	docker build -f ./build/controller/Dockerfile --build-arg ARCH="arm64v8"  -t $(LOCAL_REGISTRY)/$(LOCAL_CONTROLLER_IMAGE) .

.PHONY: clean
clean:
	rm -rf ./bin
