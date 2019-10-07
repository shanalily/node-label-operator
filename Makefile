
# Image URL to use all building/pushing image targets
IMG ?= controller:latest
E2E_SUBSCRIPTION ?= "Azure Container Service - Development"
EXTRA_ARGS :=

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

all: manager
.PHONY: all

# Run tests
test: generate fmt vet
	go test ./controller/... ./azure/... -coverprofile cover.out
.PHONY: test

# Build manager binary
manager: generate fmt vet
	go build -o bin/manager main.go
.PHONY: manager

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet
	go run ./main.go
.PHONY: run

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests
	kustomize build config/default | kubectl apply -f -
.PHONY: deploy

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..."
.PHONY: manifests

# Run go fmt against code
fmt:
	go fmt ./...
.PHONY: fmt

# Run go vet against code
vet:
	go vet ./...
.PHONY: vet

lint:
	golangci-lint run -j 2 $(EXTRA_ARGS)
.PHONY: lint

# e2e-setup:

e2e-test:
	go test ./tests/e2e/... -timeout 0 -v -run Test/TestARMTagToNodeLabel
.PHONY: e2e-run-tests

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile=./hack/boilerplate.go.txt
.PHONY: generate

# Build the docker image
docker-build:
	docker build . -t ${IMG}
	@echo "updating kustomize image patch file for manager resource"
	sed -i'' -e 's@image: .*@image: '"${IMG}"'@' ./config/default/manager_image_patch.yaml
.PHONY: docker-build

# Push the docker image
docker-push:
	docker push ${IMG}
.PHONY: docker-push

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.2.0-beta.4
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif
.PHONY: controller-gen

# given a cluster with an identity, this should work
quickstart:
	sed 's/<sub-id>/'"${AZURE_SUBSCRIPTION_ID}"'/g' samples/quickstart.yaml | \
		sed 's/<resource-group>/'"${AZURE_RESOURCE_GROUP}"'/g' | \
		sed 's/<identity-name>/'"${AZURE_IDENTITY}"'/g' | \
    	sed 's/<client-id>/'"${AZURE_IDENTITY_CLIENT_ID}"'/g' \
		> config/quickstart/quickstarttmp.yaml
	sed 's/<binding-name>/'"${AZURE_IDENTITY}"'-binding/g' config/quickstart/quickstarttmp.yaml | \
		sed 's/<identity-name>/'"${AZURE_IDENTITY}"'/g' | \
		sed 's/<selector-name>/node-label-operator/g' \
		> config/quickstart/quickstart.yaml
	kubectl apply -f config/quickstart/quickstart.yaml
	rm config/quickstart/quickstarttmp.yaml
	# kustomize build config/quickstart | kubectl apply -f -
.PHONY: quickstart
