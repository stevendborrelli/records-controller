CONTROLLER_GEN ?= $(CURDIR)/bin/controller-gen
IMAGE ?= records-controller:dev
CLUSTER ?= records

.PHONY: generate test test-unit run install deploy undeploy docker-build kind-e2e kind-delete

generate: ## Regenerate deepcopy code, CRDs, and RBAC.
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths=./api/...
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:artifacts:config=config/crd
	$(CONTROLLER_GEN) rbac:roleName=records-controller paths=./internal/... output:rbac:artifacts:config=config/rbac

test-unit: ## Run tests that do not need an API server.
	go test ./internal/digest/... ./internal/structural/... ./internal/validate/... -count=1

test: ## Run all tests, including envtest integration tests.
	go test ./... -count=1

install: ## Install the CRDs into the current kubectl context.
	kubectl apply -k config/crd

run: ## Run the controller locally against the current kubectl context.
	go run ./cmd/records-controller

docker-build: ## Build the controller image.
	docker build -t $(IMAGE) .

deploy: ## Deploy the CRDs and controller to the current kubectl context.
	kubectl apply -k config/default
	kubectl -n records-system set image deployment/records-controller manager=$(IMAGE)

undeploy: ## Remove the controller and CRDs from the current kubectl context.
	kubectl delete -k config/default --ignore-not-found

kind-e2e: ## Create a kind cluster, deploy, and run end-to-end checks.
	CLUSTER=$(CLUSTER) IMAGE=$(IMAGE) ./hack/kind-e2e.sh

kind-delete: ## Delete the kind cluster.
	kind delete cluster --name $(CLUSTER)
