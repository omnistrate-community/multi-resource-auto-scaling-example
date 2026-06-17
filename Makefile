DOCKER_PLATFORM=linux/arm64

# Build info
CGO_ENABLED=0

SERVICE_NAME=multi-resource-auto-scaling
SERVICE_PLAN=multi-resource-auto-scaling
MAIN_RESOURCE_NAME=controller
ENVIRONMENT=Dev
AWS_CLOUD_PROVIDER=aws
AWS_REGION=ap-south-1
GCP_CLOUD_PROVIDER=gcp
GCP_REGION=us-central1
AZURE_CLOUD_PROVIDER=azure
AZURE_REGION=eastus2

# Load variables from .env if it exists
ifneq (,$(wildcard .env))
    include .env
    export $(shell sed 's/=.*//' .env)
endif


.PHONY: all
all: tidy build unit-test lint

.PHONY: tidy
tidy:
	echo "Tidy dependency modules"
	go mod tidy

.PHONY: download
download:
	echo "Download dependency modules"
	go mod download

.PHONY: build
build:
	echo "Building go binaries for service"
	go build -o controller ./cmd/controller.go

.PHONY: unit-test
unit-test: 
	echo "Running unit tests for service"
	go test ./... -skip ./test/...

.PHONY: lint-install
lint-install: 
	echo "Installing golangci-lint"
	brew install golangci-lint
	brew upgrade golangci-lint	

.PHONY: lint
lint:
	echo "Running checks for service"
	golangci-lint run ./internal/... 
	golangci-lint run ./cmd/...

.PHONY: sec-install
sec-install: 
	echo "Installing gosec"
	go install github.com/securego/gosec/v2/cmd/gosec@latest

.PHONY: sec
sec: 
	echo "Security scanning for service"
	gosec --quiet ./...

.PHONY: update-dependencies
update-dependencies:
	echo "Updating dependencies"
	go get -t -u ./...
	go mod tidy

.PHONY: run
run:
	echo "Running service" && \
    export DRY_RUN=true && \
    export LOG_LEVEL=debug && \
    export LOG_FORMAT=pretty && \
	export AUTOSCALER_TARGET_RESOURCES="api,worker" && \
	go run ./cmd/controller.go

.PHONY: docker-build
docker-build:
	docker buildx build --platform=${DOCKER_PLATFORM} -f ./build/Dockerfile -t multi-resource-auto-scaling-controller:latest . 

.PHONY: docker-run
docker-run: docker-build
	docker run -e DRY_RUN=true -e LOG_LEVEL=debug -e LOG_FORMAT=pretty -e AUTOSCALER_TARGET_RESOURCES="api,worker" --rm -it multi-resource-auto-scaling-controller:latest

.PHONY: install-ctl
install-ctl:
	@brew tap omnistrate/tap
	@brew install omnistrate/tap/omnistrate-ctl

.PHONY: upgrade-ctl
upgrade-ctl:
	@brew upgrade omnistrate/tap/omnistrate-ctl
	
.PHONY: login
login:
	@cat ./.omnistrate.password | omnistrate-ctl login --email $(OMNISTRATE_EMAIL) --password-stdin

.PHONY: release
release:
	@omnistrate-ctl build -f omnistrate-compose.yaml --product-name ${SERVICE_NAME}  --environment ${ENVIRONMENT} --environment-type ${ENVIRONMENT} --release-as-preferred

.PHONY: create-aws
create-aws:
	@omnistrate-ctl instance create --environment ${ENVIRONMENT} --cloud-provider ${AWS_CLOUD_PROVIDER} --region ${AWS_REGION} --plan ${SERVICE_PLAN} --service ${SERVICE_NAME} --resource ${MAIN_RESOURCE_NAME} 

.PHONY: create-gcp
create-gcp:
	@omnistrate-ctl instance create --environment ${ENVIRONMENT} --cloud-provider ${GCP_CLOUD_PROVIDER} --region ${GCP_REGION} --plan ${SERVICE_PLAN} --service ${SERVICE_NAME} --resource ${MAIN_RESOURCE_NAME}

.PHONY: create-azure
create-azure:
	@omnistrate-ctl instance create --environment ${ENVIRONMENT} --cloud-provider ${AZURE_CLOUD_PROVIDER} --region ${AZURE_REGION} --plan ${SERVICE_PLAN} --service ${SERVICE_NAME} --resource ${MAIN_RESOURCE_NAME}

.PHONY: list
list:
	@omnistrate-ctl instance list --filter=service:${SERVICE_NAME},plan:${SERVICE_PLAN} --output json

.PHONY: delete-all
delete-all:
	@echo "Deleting all instances..."
	@for id in $$(omnistrate-ctl instance list --filter=service:${SERVICE_NAME},plan:${SERVICE_PLAN} --output json | jq -r '.[].instance_id'); do \
		echo "Deleting instance: $$id"; \
		omnistrate-ctl instance delete $$id; \
	done

.PHONY: destroy
destroy: delete-all-wait
	@echo "Destroying service: ${SERVICE_NAME}..."
	@omnistrate-ctl service delete ${SERVICE_NAME}

.PHONY: delete-all-wait
delete-all-wait:
	@echo "Deleting all instances and waiting for completion..."
	@instances_to_delete=$$(omnistrate-ctl instance list --filter=service:${SERVICE_NAME},plan:${SERVICE_PLAN} --output json | jq -r '.[].instance_id'); \
	if [ -n "$$instances_to_delete" ]; then \
		for id in $$instances_to_delete; do \
			echo "Deleting instance: $$id"; \
			omnistrate-ctl instance delete $$id; \
		done; \
		echo "Waiting for instances to be deleted..."; \
		while true; do \
			remaining=$$(omnistrate-ctl instance list --filter=service:${SERVICE_NAME},plan:${SERVICE_PLAN} --output json | jq -r '.[].instance_id'); \
			if [ -z "$$remaining" ]; then \
				echo "All instances deleted successfully"; \
				break; \
			fi; \
			echo "Still waiting for deletion to complete..."; \
			sleep 10; \
		done; \
	else \
		echo "No instances found to delete"; \
	fi
