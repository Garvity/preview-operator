SHELL := /bin/bash
IMAGE ?= preview-operator:dev
VERSION ?= dev
CONTROLLER_GEN_VERSION := v0.22.0
ENVTEST_VERSION := v0.25.1
ENVTEST_K8S_VERSION := 1.37.0

.PHONY: help generate fmt vet test build manifests verify-generated install deploy run docker-build envtest test-integration test-e2e verify
help:
	@echo 'Targets: generate fmt vet test build manifests verify-generated install deploy run docker-build envtest test-integration test-e2e verify'
generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION) object crd paths=./api/... output:crd:artifacts:config=config/crd/bases
fmt:
	gofmt -w api cmd internal test
vet:
	go vet ./...
test:
	go test -race -coverprofile=coverage.out ./api/... ./internal/...
build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o bin/manager ./cmd/manager
manifests:
	kubectl kustomize config/default
verify-generated: generate
	git diff --exit-code -- api/v1alpha1/zz_generated.deepcopy.go config/crd/bases
install:
	kubectl apply -k config/crd
deploy:
	kubectl apply -k config/default
run:
	go run ./cmd/manager --watch-namespace=preview-system --leader-elect=false
docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .
envtest:
	@mkdir -p bin
	GOBIN=$(CURDIR)/bin go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)
	./bin/setup-envtest use $(ENVTEST_K8S_VERSION) --bin-dir $(CURDIR)/.envtest -p path
test-integration:
	KUBEBUILDER_ASSETS="$$(./bin/setup-envtest use $(ENVTEST_K8S_VERSION) --bin-dir $(CURDIR)/.envtest -p path)" go test -race -tags=integration -count=1 -v ./test/integration/...
test-e2e:
	./hack/e2e.sh
verify: vet test build verify-generated
