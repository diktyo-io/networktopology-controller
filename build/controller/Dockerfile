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

# Build stage: always run on the host platform for speed, cross-compile for TARGETARCH
FROM --platform=$BUILDPLATFORM golang:1.21 AS builder

ARG TARGETARCH

WORKDIR /go/src/networktopology-controller
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -ldflags '-w' -o bin/controller cmd/controller/controller.go

# Final stage: use the target platform's alpine image
FROM alpine:3.18

COPY --from=builder /go/src/networktopology-controller/bin/controller /bin/controller

WORKDIR /bin
CMD ["controller"]
