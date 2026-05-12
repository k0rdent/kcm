NAMESPACE ?= kcm-system
VERSION ?= $(shell git describe --tags --always)
VERSION := $(patsubst v%,%,$(VERSION))
FQDN_VERSION = $(subst .,-,$(VERSION))
# Image URL to use all building/pushing image targets
IMG ?= localhost/kcm/controller:latest
IMG_REPO = $(shell echo $(IMG) | cut -d: -f1)
IMG_TAG = $(shell echo $(IMG) | cut -d: -f2)

IMG_TELEMETRY ?= localhost/kcm/telemetry:latest
IMG_TELEMETRY_REPO = $(shell echo $(IMG_TELEMETRY) | cut -d: -f1)
IMG_TELEMETRY_TAG  = $(shell echo $(IMG_TELEMETRY) | cut -d: -f2)

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

KCM_STABLE_VERSION = $(shell git ls-remote --tags --sort v:refname --exit-code --refs https://github.com/k0rdent/kcm | grep -v -e "-rc[0-9]\+$$" | tail -n1 | cut -d '/' -f3)

HOSTOS := $(shell go env GOHOSTOS)
HOSTARCH := $(shell go env GOHOSTARCH)

# aws default values
AWS_REGION ?= us-east-2

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

TEMPLATES_DIR := templates
PROVIDER_TEMPLATES_DIR := $(TEMPLATES_DIR)/provider

.PHONY: all
all: build ## Build default project artifacts.

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: MANAGEMENT_CRDS_DIR := $(PROVIDER_TEMPLATES_DIR)/kcm/templates/crds
manifests: REGIONAL_CRDS_DIR := $(PROVIDER_TEMPLATES_DIR)/kcm-regional/templates/crds
manifests: controller-gen yq ## Generate CustomResourceDefinition objects.
	$(CONTROLLER_GEN) crd paths="./..." output:crd:artifacts:config=$(MANAGEMENT_CRDS_DIR)
	find $(MANAGEMENT_CRDS_DIR) -maxdepth 1 -name "*.yaml" -exec $(YQ) eval -i '.metadata.annotations["helm.sh/resource-policy"] = "keep"' {} \;
	mkdir -p $(REGIONAL_CRDS_DIR)
	mv $(MANAGEMENT_CRDS_DIR)/k0rdent.mirantis.com_providerinterfaces.yaml $(REGIONAL_CRDS_DIR)
	find $(REGIONAL_CRDS_DIR) -maxdepth 1 -name "*.yaml" -exec $(YQ) eval -i '.metadata.annotations["helm.sh/resource-policy"] = "keep"' {} \;

.PHONY: generate
generate: controller-gen ## Generate DeepCopy and related API boilerplate.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt",year="$(shell date +%Y)" paths="./..."

.PHONY: generate-release
generate-release: yq ## Render release object metadata from VERSION.
	@release_file="$(PROVIDER_TEMPLATES_DIR)/kcm-templates/files/release.yaml"; \
	if [ -n "$$OUTPUT" ]; then \
		$(YQ) eval \
			'.spec.version = "$(VERSION)" | .metadata.name = "kcm-$(FQDN_VERSION)" | .spec.kcm.template = "kcm-$(FQDN_VERSION)" | .spec.regional.template = "kcm-regional-$(FQDN_VERSION)"' \
			"$$release_file" > "$$OUTPUT"; \
	else \
		$(YQ) eval -i \
			'.spec.version = "$(VERSION)" | .metadata.name = "kcm-$(FQDN_VERSION)" | .spec.kcm.template = "kcm-$(FQDN_VERSION)" | .spec.regional.template = "kcm-regional-$(FQDN_VERSION)"' \
			"$$release_file"; \
	fi

.PHONY: set-kcm-version
set-kcm-version: yq ## Update chart and image tags to VERSION.
	$(YQ) eval '.version = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm-regional/Chart.yaml
	$(YQ) eval -i \
		'.version = "$(VERSION)" | (.dependencies[] | select(.name == "kcm-regional") | .version) = "$(VERSION)"' \
		$(PROVIDER_TEMPLATES_DIR)/kcm/Chart.yaml
	$(YQ) eval '.version = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm-templates/Chart.yaml
	$(YQ) eval '.image.tag = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm/values.yaml
	$(YQ) eval '.telemetry.controller.image.tag = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm-regional/values.yaml
	@$(MAKE) generate-release

.PHONY: set-kcm-repo
set-kcm-repo: yq ## Update controller image repositories in values files.
	$(YQ) eval '.image.repository = "$(IMG_REPO)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm/values.yaml
	$(YQ) eval '.telemetry.controller.image.repository = "$(IMG_TELEMETRY_REPO)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm-regional/values.yaml

.PHONY: set-templates-repo
set-templates-repo: yq ## Update templates repository URL in values files.
	$(YQ) eval '.controller.templatesRepoURL = "$(REGISTRY_REPO)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm/values.yaml

.PHONY: kcm-chart-release
kcm-chart-release: set-kcm-version templates-generate ## Generate KCM chart content for publishing.

# TODO: combine all of the bash/sh files into a single one
.PHONY: templates-generate
templates-generate: ## Generate provider/cluster templates.
	@$(SHELL) hack/templates.sh

.PHONY: bump-chart-version
bump-chart-version: yq ## Bump template chart versions.
	@YQ=$(YQ) $(SHELL) hack/chart-version.bash

.PHONY: update-release
update-release: yq ## Refresh release manifests.
	@YQ=$(YQ) $(SHELL) hack/update-release.bash

.PHONY: update-dev-confs
update-dev-confs: yq ## Refresh development configuration files.
	@YQ=$(YQ) $(SHELL) hack/update-dev-configs.bash

.PHONY: capo-orc-fetch
capo-orc-fetch: CAPO_DIR := $(PROVIDER_TEMPLATES_DIR)/cluster-api-provider-openstack
capo-orc-fetch: CAPO_ORC_VERSION := 2.1.0
capo-orc-fetch: CAPO_ORC_TEMPLATE := "$(CAPO_DIR)/templates/orc.yaml"
capo-orc-fetch: yq ## Fetch and template ORC installation manifest.
	@CAPO_ORC_VERSION=$(CAPO_ORC_VERSION) CAPO_ORC_TEMPLATE=$(CAPO_ORC_TEMPLATE) YQ=$(YQ) $(SHELL) hack/capo-orc-fetch.bash

.PHONY: generate-all
generate-all: generate manifests schema-charts bump-chart-version templates-generate update-release add-license capo-orc-fetch update-dev-confs ## Run all generation/update steps.

##@ Quality

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: fix
fix: ## Run go fix against code.
	go fix ./...

.PHONY: tidy
tidy: ## Run go mod tidy.
	go mod tidy

##@ Test

.PHONY: test
test: generate-all envtest tidy external-crd ## Run unit and env tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# Utilize Kind or modify the e2e tests to load the image locally, enabling
# compatibility with other vendors.
.PHONY: test-e2e
test-e2e: ## Run e2e tests using a Kind management cluster.
	@KIND_VERSION=$(KIND_VERSION) GINKGO_LABEL_FILTER="$(GINKGO_LABEL_FILTER)" GITHUB_ACTIONS="$(GITHUB_ACTIONS)" $(SHELL) hack/test-e2e.bash

.PHONY: load-e2e-config
load-e2e-config: yq ## Decode and validate E2E config override from E2E_CONFIG_B64.
	@if [ -z "$(E2E_CONFIG_B64)" ]; then \
		echo "E2E_CONFIG_B64 is empty, the default configuration from test/e2e/config/config.yaml will be used"; \
		exit 0; \
	fi; \
	config_content="$$(echo -n "$(E2E_CONFIG_B64)" | base64 -d)"; \
	echo "Validating provided configuration..."; \
	if ! echo "$$config_content" | $(YQ) eval > /dev/null 2>&1; then \
		echo "Invalid YAML configuration provided:"; \
		echo "$$config_content"; \
		exit 1; \
	fi; \
	echo "$$config_content" > test/e2e/config/config.yaml; \
	echo "Testing configuration was overwritten:"; \
	cat test/e2e/config/config.yaml

##@ Lint

.PHONY: lint
lint: golangci-lint fmt vet fix ## Run linter and Go quality checks.
	@$(GOLANGCI_LINT) run --timeout=$(GOLANGCI_LINT_TIMEOUT)

.PHONY: lint-fix
lint-fix: golangci-lint fmt vet fix ## Run linter with automatic fixes.
	@$(GOLANGCI_LINT) run --fix

.PHONY: add-license
add-license: addlicense ## Add/update file headers.
	$(ADDLICENSE) -c "" -ignore ".github/**" -ignore "config/**" -ignore "templates/**" -ignore "bin/**" -ignore ".*" .

##@ Package

CHARTS_PACKAGE_DIR ?= $(LOCALBIN)/charts
$(CHARTS_PACKAGE_DIR): | $(LOCALBIN)
	rm -rf $(CHARTS_PACKAGE_DIR)
	mkdir -p $(CHARTS_PACKAGE_DIR)

EXTENSION_CHARTS_PACKAGE_DIR ?= $(LOCALBIN)/charts/extensions
$(EXTENSION_CHARTS_PACKAGE_DIR): | $(LOCALBIN)
	mkdir -p $(EXTENSION_CHARTS_PACKAGE_DIR)

IMAGES_PACKAGE_DIR ?= $(LOCALBIN)/images
$(IMAGES_PACKAGE_DIR): | $(LOCALBIN)
	rm -rf $(IMAGES_PACKAGE_DIR)
	mkdir -p $(IMAGES_PACKAGE_DIR)

TEMPLATE_FOLDERS = $(patsubst $(TEMPLATES_DIR)/%,%,$(wildcard $(TEMPLATES_DIR)/*))
TEST_CHARTS = $(sort $(patsubst %/tests/,%,$(dir $(wildcard $(TEMPLATES_DIR)/*/*/tests/*_test.yaml))))

.PHONY: helm-package
helm-package: $(CHARTS_PACKAGE_DIR) $(EXTENSION_CHARTS_PACKAGE_DIR) helm ## Package all template charts.
	@$(MAKE) $(patsubst %,package-%-tmpl,$(TEMPLATE_FOLDERS))

package-%-tmpl:
	@$(MAKE) TEMPLATES_SUBDIR=$(TEMPLATES_DIR)/$* $(patsubst %,package-chart-%,$(shell ls $(TEMPLATES_DIR)/$*))

package-chart-%: lint-chart-%
	$(HELM) package --destination $(CHARTS_PACKAGE_DIR) $(TEMPLATES_SUBDIR)/$*

.PHONY: lint-charts
lint-charts: helm lint-quoted-values test-charts ## Run quote checks plus Helm dependency update/lint and chart unit tests.
	@$(MAKE) $(patsubst %,lint-%-tmpl,$(TEMPLATE_FOLDERS))

.PHONY: test-charts
test-charts: helm-plugin-unittest ## Run Helm unit tests for charts that define tests/*_test.yaml suites.
	@if [ -z "$(TEST_CHARTS)" ]; then \
		echo "No Helm unittest suites found."; \
		exit 0; \
	fi
	@status=0; \
	for chart in $(TEST_CHARTS); do \
		echo "Running helm unittest in $$chart"; \
		$(HELM) dependency update "$$chart" >/dev/null || status=$$?; \
		$(HELM) unittest "$$chart" -f 'tests/*_test.yaml' -q || status=$$?; \
	done; \
	exit $$status

.PHONY: lint-quoted-values
lint-quoted-values: yq ## Ensure string .Values YAML interpolations are quoted in template charts.
	@YQ=$(YQ) $(SHELL) hack/lint-quoted-values.bash

lint-%-tmpl:
	@$(MAKE) TEMPLATES_SUBDIR=$(TEMPLATES_DIR)/$* $(patsubst %,lint-chart-%,$(shell ls $(TEMPLATES_DIR)/$*))

lint-chart-%:
	@if [ "$*" == "kcm" ]; then \
		$(HELM) dependency update $(TEMPLATES_SUBDIR)/kcm-regional; \
	fi
	$(HELM) dependency update $(TEMPLATES_SUBDIR)/$*
	$(HELM) lint --strict $(TEMPLATES_SUBDIR)/$*

.PHONY: schema-charts
schema-charts: helm-plugin-schema ## Generate values schemas for charts.
	@$(MAKE) $(patsubst %,schema-%-tmpl,$(TEMPLATE_FOLDERS))

schema-%-tmpl:
	@$(MAKE) TYPE=$* TEMPLATES_SUBDIR=$(TEMPLATES_DIR)/$* $(patsubst %,schema-chart-%,$(shell ls $(TEMPLATES_DIR)/$*))

schema-chart-%:
	@cd $(TEMPLATES_SUBDIR)/$* && { [ ! -f values.yaml ] || (echo -n "$(TEMPLATES_SUBDIR)/$*: " && $(HELM) schema --draft 2020 --indent 2 --schema-root.description='A KCM $(TYPE) $* template'); }

##@ Build

COMPONENTS := controller telemetry

BIN_controller ?= manager
PKG_controller ?= ./cmd/main.go

BIN_telemetry ?= telemetry
PKG_telemetry ?= ./cmd/telemetry

# Map component -> image ref
define IMG_for
$(if $(filter $1,controller),$(IMG),\
$(if $(filter $1,telemetry),$(IMG_TELEMETRY),))
endef

LD_FLAGS?= -s -w
LD_FLAGS += -X github.com/K0rdent/kcm/internal/build.Version=$(VERSION)
LD_FLAGS += -X github.com/K0rdent/kcm/internal/build.Commit=$(shell git rev-parse --verify HEAD)
LD_FLAGS += -X github.com/K0rdent/kcm/internal/build.Name="kcm"
LD_FLAGS += -X github.com/K0rdent/kcm/internal/build.Time=$(shell git show -s --date=format:'%Y-%m-%dT%H:%M:%S%z' --format=%cd $$(git rev-parse --verify HEAD))
LD_FLAGS += -X github.com/K0rdent/kcm/internal/telemetry.segmentToken=$(SEGMENT_TOKEN)

.PHONY: build
build: generate-all $(addprefix build-,$(COMPONENTS)) ## Build all component binaries into ./bin.

.PHONY: build-%
build-%: ## Build a single component binary, e.g. `make build-telemetry`
	go build -ldflags="$(LD_FLAGS)" -o bin/$(BIN_$*) $(PKG_$*)

.PHONY: run
run: run-controller ## Run default component (manager) locally.

.PHONY: run-%
run-%: generate-all ## Run a single component from your host, e.g. `make run-telemetry`
	go run $(PKG_$*)

# If you wish to build images targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: $(addprefix docker-,$(addsuffix -build,$(COMPONENTS))) ## Build all container images.

.PHONY: docker-%-build
docker-%-build:  ## Build a single component image, e.g. `make docker-controller-build`
	$(CONTAINER_TOOL) build \
		-t $(call IMG_for,$*) \
		--build-arg LD_FLAGS="$(LD_FLAGS)" \
		$(if $(TARGETOS),--build-arg TARGETOS=$(TARGETOS),) \
		$(if $(TARGETARCH),--build-arg TARGETARCH=$(TARGETARCH),) \
		--build-arg BIN=$(BIN_$*) \
		--build-arg PKG=$(PKG_$*) \
		.

.PHONY: docker-push
docker-push: $(addprefix docker-,$(addsuffix -push,$(COMPONENTS)))  ## Push all container images.

.PHONY: docker-%-push
docker-%-push: ## Push a single component image, e.g. `make docker-telemetry-push`
	$(CONTAINER_TOOL) push $(call IMG_for,$*)

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: $(addprefix docker-,$(addsuffix -buildx,$(COMPONENTS))) ## Build and push multi-platform images.

.PHONY: docker-%-buildx
docker-%-buildx: ## Build+push a single component image for $(PLATFORMS), e.g. `make docker-telemetry-buildx`
	- $(CONTAINER_TOOL) buildx create --name project-v3-builder
	$(CONTAINER_TOOL) buildx use project-v3-builder
	- $(CONTAINER_TOOL) buildx build \
		--push \
		--platform=$(PLATFORMS) \
		--tag $(call IMG_for,$*) \
		--build-arg LD_FLAGS="$(LD_FLAGS)" \
		$(if $(TARGETOS),--build-arg TARGETOS=$(TARGETOS),) \
		$(if $(TARGETARCH),--build-arg TARGETARCH=$(TARGETARCH),) \
		--build-arg BIN=$(BIN_$*) \
		--build-arg PKG=$(PKG_$*) \
		-f Dockerfile \
		.
	- $(CONTAINER_TOOL) buildx rm project-v3-builder

##@ Deployment

KCM_HELM_RELEASE_NAME ?= kcm
KIND_CLUSTER_NAME ?= kcm-dev
KIND_NETWORK ?= kind
REGISTRY_NAME ?= kcm-local-registry
REGISTRY_PORT ?= 5001
REGISTRY_REPO ?= oci://127.0.0.1:$(REGISTRY_PORT)/charts
DEV_PROVIDER ?= aws
VALIDATE_CLUSTER_UPGRADE_PATH ?= true
REGISTRY_IS_OCI = $(if $(filter oci://%,$(REGISTRY_REPO)),true,false)

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: kind-deploy
kind-deploy: kind ## Create a kind cluster if it does not exist.
	@if ! $(KIND) get clusters | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		if [ -n "$(KIND_CONFIG_PATH)" ]; then \
			$(KIND) create cluster -n $(KIND_CLUSTER_NAME) --config "$(KIND_CONFIG_PATH)"; \
		else \
			$(KIND) create cluster -n $(KIND_CLUSTER_NAME); \
		fi; \
		$(CONTAINER_TOOL) exec "$(KIND_CLUSTER_NAME)-control-plane" bash -ec \
			"echo 'fs.inotify.max_user_watches=524288' >> /etc/sysctl.conf; \
			 echo 'fs.inotify.max_user_instances=512' >> /etc/sysctl.conf; \
			 sysctl -p /etc/sysctl.conf"; \
	fi

.PHONY: kind-undeploy
kind-undeploy: kind ## Delete the kind cluster if it exists.
	@if $(KIND) get clusters | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		$(KIND) delete cluster --name $(KIND_CLUSTER_NAME); \
	fi

.PHONY: registry-deploy
registry-deploy: ## Start and connect to the local OCI registry.
	@if [ ! "$$($(CONTAINER_TOOL) ps -aq -f name=$(REGISTRY_NAME))" ]; then \
		echo "Starting new local registry container $(REGISTRY_NAME)"; \
		$(CONTAINER_TOOL) run -d --restart=always -p "127.0.0.1:$(REGISTRY_PORT):5000" --network bridge --name "$(REGISTRY_NAME)" registry:2; \
	fi; \
	if [ "$$($(CONTAINER_TOOL) inspect -f='{{json .NetworkSettings.Networks.$(KIND_NETWORK)}}' $(REGISTRY_NAME))" = 'null' ]; then \
		$(CONTAINER_TOOL) network connect $(KIND_NETWORK) $(REGISTRY_NAME); \
	fi

.PHONY: registry-undeploy
registry-undeploy: ## Remove local OCI registry container.
	@if [ "$$($(CONTAINER_TOOL) ps -aq -f name=$(REGISTRY_NAME))" ]; then \
		echo "Removing local registry container $(REGISTRY_NAME)"; \
		$(CONTAINER_TOOL) rm -f "$(REGISTRY_NAME)"; \
	fi

.PHONY: kcm-deploy
kcm-deploy: helm ## Deploy/upgrade KCM Helm chart into namespace.
	$(HELM) upgrade --values $(KCM_VALUES) --reuse-values --install --create-namespace $(KCM_HELM_RELEASE_NAME) $(PROVIDER_TEMPLATES_DIR)/kcm -n $(NAMESPACE)

.PHONY: dev-deploy
dev-deploy: DEPLOYMENT_NAME := $(if $(findstring kcm,$(KCM_HELM_RELEASE_NAME)),$(KCM_HELM_RELEASE_NAME)-controller-manager,$(KCM_HELM_RELEASE_NAME)-kcm-controller-manager)
dev-deploy: yq ## Configure and deploy KCM chart for development.
	@$(YQ) eval -i '.image.repository = "$(IMG_REPO)"' config/dev/kcm_values.yaml
	@$(YQ) eval -i '.regional.telemetry.controller.image.repository = "$(IMG_TELEMETRY_REPO)"' config/dev/kcm_values.yaml
	@$(YQ) eval -i '.image.tag = "$(IMG_TAG)"' config/dev/kcm_values.yaml
	@$(YQ) eval -i '.regional.telemetry.controller.image.tag = "$(IMG_TELEMETRY_TAG)"' config/dev/kcm_values.yaml
	@if [ "$(REGISTRY_REPO)" = "oci://127.0.0.1:$(REGISTRY_PORT)/charts" ]; then \
		$(YQ) eval -i '.controller.templatesRepoURL = "oci://$(REGISTRY_NAME):5000/charts"' config/dev/kcm_values.yaml; \
	else \
		$(YQ) eval -i '.controller.templatesRepoURL = "$(REGISTRY_REPO)"' config/dev/kcm_values.yaml; \
	fi;
	@$(YQ) eval -i '.controller.validateClusterUpgradePath = $(VALIDATE_CLUSTER_UPGRADE_PATH)' config/dev/kcm_values.yaml
	@if [ "$(CI_TELEMETRY)" = "true" ]; then \
		echo "Enabling online telemetry for CI environment"; \
		$(YQ) eval -i '.regional.telemetry.mode = "online"' config/dev/kcm_values.yaml; \
		$(YQ) eval -i '.regional.telemetry.interval = "10m"' config/dev/kcm_values.yaml; \
	fi;
	$(MAKE) kcm-deploy KCM_VALUES=config/dev/kcm_values.yaml
	$(KUBECTL) rollout restart -n $(NAMESPACE) deployment/$(DEPLOYMENT_NAME)

.PHONY: dev-undeploy
dev-undeploy: ## Remove KCM release from namespace.
	$(HELM) delete -n $(NAMESPACE) $(KCM_HELM_RELEASE_NAME)

.PHONY: helm-push
helm-push: helm-package ## Push packaged charts if version is not already present.
	@HELM=$(HELM) CHARTS_PACKAGE_DIR=$(CHARTS_PACKAGE_DIR) REGISTRY_REPO=$(REGISTRY_REPO) REGISTRY_IS_OCI=$(REGISTRY_IS_OCI) REGISTRY_USERNAME="$(REGISTRY_USERNAME)" REGISTRY_PASSWORD="$(REGISTRY_PASSWORD)" $(SHELL) hack/helm-push.bash

# kind doesn't support load docker-image if the container tool is podman
# https://github.com/kubernetes-sigs/kind/issues/2038
.PHONY: dev-push
dev-push: docker-build helm-push ## Build/push images and load them into kind.
	@if [ "$(CONTAINER_TOOL)" = "podman" ]; then \
		$(KIND) load image-archive --name $(KIND_CLUSTER_NAME) <($(CONTAINER_TOOL) save $(IMG)); \
		$(KIND) load image-archive --name $(KIND_CLUSTER_NAME) <($(CONTAINER_TOOL) save $(IMG_TELEMETRY)); \
	else \
		$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER_NAME); \
		$(KIND) load docker-image $(IMG_TELEMETRY) --name $(KIND_CLUSTER_NAME); \
	fi; \

.PHONY: dev-templates
dev-templates: templates-generate ## Apply generated templates to the management cluster.
	$(KUBECTL) -n $(NAMESPACE) apply --force -f $(PROVIDER_TEMPLATES_DIR)/kcm-templates/files/templates

KCM_REPO_URL ?= oci://ghcr.io/k0rdent/kcm/charts
KCM_REPO_NAME ?= kcm

.PHONY: stable-templates
stable-templates: yq ## Apply templates from latest stable KCM release.
	@KUBECTL=$(KUBECTL) YQ=$(YQ) NAMESPACE=$(NAMESPACE) KCM_REPO_NAME=$(KCM_REPO_NAME) KCM_REPO_URL=$(KCM_REPO_URL) KCM_STABLE_VERSION=$(KCM_STABLE_VERSION) $(SHELL) hack/stable-templates.bash

.PHONY: dev-release
dev-release: yq ## Apply release object for current VERSION.
	@$(YQ) e ".spec.version = \"${VERSION}\"" $(PROVIDER_TEMPLATES_DIR)/kcm-templates/files/release.yaml | $(KUBECTL) -n $(NAMESPACE) apply -f -

.PHONY: dev-apply
dev-apply: kind-deploy registry-deploy dev-push dev-deploy dev-templates dev-release ## Deploy full local development environment.

.PHONY: dev-upgrade
dev-upgrade: VALUES_FILE ?= config/dev/kcm_values.yaml
dev-upgrade: READINESS_TIMEOUT ?= 30m
dev-upgrade: yq generate-all dev-push dev-templates ## Upgrade dev environment and wait until Management is ready.
	@YQ=$(YQ) KUBECTL=$(KUBECTL) VERSION=$(VERSION) FQDN_VERSION=$(FQDN_VERSION) PROVIDER_TEMPLATES_DIR=$(PROVIDER_TEMPLATES_DIR) VALUES_FILE=$(VALUES_FILE) NAMESPACE=$(NAMESPACE) READINESS_TIMEOUT=$(READINESS_TIMEOUT) $(SHELL) hack/dev-upgrade.bash

PUBLIC_REPO ?= false

.PHONY: test-apply
test-apply: kind-deploy ## Prepare environment and deploy artifacts for test runs.
	@if [ "$(PUBLIC_REPO)" != "true" ]; then \
	  $(MAKE) registry-deploy dev-push; \
	else \
	  $(MAKE) lint-charts kcm-chart-release; \
	fi; \
	$(MAKE) dev-deploy dev-templates dev-release

.PHONY: dev-destroy
dev-destroy: kind-undeploy registry-undeploy ## Destroy local development environment.

.PHONY: dev-mcluster-apply
dev-mcluster-apply: envsubst ## Create dev managed cluster for selected provider.
ifeq ($(CLUSTER_NAME_SUFFIX),)
	$(error CLUSTER_NAME_SUFFIX must be set)
endif
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml | $(KUBECTL) apply -f -

.PHONY: dev-mcluster-delete
dev-mcluster-delete: envsubst ## Delete dev managed cluster for selected provider.
ifeq ($(CLUSTER_NAME_SUFFIX),)
	$(error CLUSTER_NAME_SUFFIX must be set)
endif
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml | $(KUBECTL) delete -f -

.PHONY: dev-creds-apply
dev-creds-apply: dev-$(DEV_PROVIDER)-creds ## Apply provider credentials for DEV_PROVIDER.

.PHONY: dev-%-creds
dev-%-creds: envsubst
	@provider="$*"; \
	creds_provider="$$provider"; \
	if [ "$$creds_provider" = "eks" ]; then \
		creds_provider="aws"; \
	fi; \
	creds_file="config/dev/$$creds_provider-credentials.yaml"; \
	if [ ! -f "$$creds_file" ]; then \
		echo "Credentials file not found for provider '$$provider': $$creds_file"; \
		exit 1; \
	fi; \
	strict_flags="-no-unset"; \
	if [ "$$creds_provider" = "aws" ]; then \
		strict_flags=""; \
	fi; \
	NAMESPACE=$(NAMESPACE) $(ENVSUBST) $$strict_flags -i "$$creds_file" | $(KUBECTL) apply -f -

.PHONY: kubevirt
kubevirt: ## Install KubeVirt and CDI on current cluster.
	@KUBECTL=$(KUBECTL) $(SHELL) hack/kubevirt-install.bash

##@ Operations

.PHONY: support-bundle
support-bundle: SUPPORT_BUNDLE_OUTPUT=$(CURDIR)/support-bundle-$(shell date +"%Y-%m-%dT%H_%M_%S_%N")
support-bundle: envsubst support-bundle-cli ## Collect support bundle from the cluster.
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/support-bundle.yaml | $(SUPPORT_BUNDLE_CLI) -o $(SUPPORT_BUNDLE_OUTPUT) --debug -

.PHONY: dev-aws-nuke
dev-aws-nuke: envsubst awscli yq cloud-nuke ## Nuke AWS resources deployed by dev-mcluster-apply.
	@CLUSTER_NAME=$(CLUSTER_NAME) YQ=$(YQ) AWSCLI=$(AWSCLI) $(SHELL) $(CURDIR)/scripts/aws-nuke-ccm.sh elb
	@CLUSTER_NAME=$(CLUSTER_NAME) $(ENVSUBST) < config/dev/aws-cloud-nuke.yaml.tpl > config/dev/aws-cloud-nuke.yaml
	DISABLE_TELEMETRY=true $(CLOUDNUKE) aws --region $(AWS_REGION) --force --config config/dev/aws-cloud-nuke.yaml --resource-type vpc,eip,nat-gateway,ec2,ec2-subnet,elb,elbv2,ebs,internet-gateway,network-interface,security-group,ekscluster
	@rm config/dev/aws-cloud-nuke.yaml
	@CLUSTER_NAME=$(CLUSTER_NAME) YQ=$(YQ) AWSCLI=$(AWSCLI) $(SHELL) $(CURDIR)/scripts/aws-nuke-ccm.sh ebs

.PHONY: dev-azure-nuke
dev-azure-nuke: envsubst azure-nuke ## Nuke Azure resources deployed by dev-mcluster-apply.
	@CLUSTER_NAME=$(CLUSTER_NAME) $(ENVSUBST) -no-unset < config/dev/azure-cloud-nuke.yaml.tpl > config/dev/azure-cloud-nuke.yaml
	$(AZURENUKE) run --config config/dev/azure-cloud-nuke.yaml --force --no-dry-run
	@rm config/dev/azure-cloud-nuke.yaml

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

EXTERNAL_CRD_DIR ?= $(LOCALBIN)/crd
$(EXTERNAL_CRD_DIR): $(LOCALBIN)
	mkdir -p $(EXTERNAL_CRD_DIR)

## CRD dependencies
FLUX_SOURCE_VERSION ?= $(shell go mod edit -json | jq -r '.Require[] | select(.Path == "github.com/fluxcd/source-controller/api") | .Version')
FLUX_SOURCE_REPO_NAME ?= source-helmrepositories
FLUX_SOURCE_REPO_CRD ?= $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_REPO_NAME)-$(FLUX_SOURCE_VERSION).yaml
FLUX_SOURCE_CHART_NAME ?= source-helmchart
FLUX_SOURCE_CHART_CRD ?= $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_CHART_NAME)-$(FLUX_SOURCE_VERSION).yaml
FLUX_SOURCE_GITREPO_NAME ?= source-gitrepositories
FLUX_SOURCE_GITREPO_CRD ?= $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_GITREPO_NAME)-$(FLUX_SOURCE_VERSION).yaml
FLUX_SOURCE_BUCKET_NAME ?= source-buckets
FLUX_SOURCE_BUCKET_CRD ?= $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_BUCKET_NAME)-$(FLUX_SOURCE_VERSION).yaml
FLUX_SOURCE_OCIREPO_NAME ?= source-ocirepositories
FLUX_SOURCE_OCIREPO_CRD ?= $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_OCIREPO_NAME)-$(FLUX_SOURCE_VERSION).yaml

FLUX_HELM_VERSION ?= $(shell go mod edit -json | jq -r '.Require[] | select(.Path == "github.com/fluxcd/helm-controller/api") | .Version')
FLUX_HELM_NAME ?= helm
FLUX_HELM_CRD ?= $(EXTERNAL_CRD_DIR)/$(FLUX_HELM_NAME)-$(FLUX_HELM_VERSION).yaml

VELERO_VERSION ?= $(shell go mod edit -json | jq -r '.Require[] | select(.Path == "github.com/vmware-tanzu/velero") | .Version')
VELERO_BACKUP_NAME ?= velero.io_backups
VELERO_BACKUP_CRD ?= $(EXTERNAL_CRD_DIR)/$(VELERO_BACKUP_NAME)-$(VELERO_VERSION).yaml

SVELTOS_VERSION ?= $(shell grep 'appVersion:' $(PROVIDER_TEMPLATES_DIR)/projectsveltos/Chart.yaml | cut -d '"' -f 2)
SVELTOS_NAME ?= sveltos
SVELTOS_CRD ?= $(EXTERNAL_CRD_DIR)/$(SVELTOS_NAME)-$(SVELTOS_VERSION).yaml

IPAM_INCLUSTER_VERSION ?= $(shell go mod edit -json | jq -r '.Require[] | select(.Path == "sigs.k8s.io/cluster-api-ipam-provider-in-cluster") | .Version')
IPAM_INCLUSTER_NAME ?= ipam_in_cluster
IPAM_INCLUSTER_CRD ?= $(EXTERNAL_CRD_DIR)/$(IPAM_INCLUSTER_NAME)-$(IPAM_INCLUSTER_VERSION).yaml

$(FLUX_HELM_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(FLUX_HELM_NAME)*
	curl -s --fail https://raw.githubusercontent.com/fluxcd/helm-controller/$(FLUX_HELM_VERSION)/config/crd/bases/helm.toolkit.fluxcd.io_helmreleases.yaml > $(FLUX_HELM_CRD)

$(FLUX_SOURCE_CHART_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_CHART_NAME)*
	curl -s --fail https://raw.githubusercontent.com/fluxcd/source-controller/$(FLUX_SOURCE_VERSION)/config/crd/bases/source.toolkit.fluxcd.io_helmcharts.yaml > $(FLUX_SOURCE_CHART_CRD)

$(FLUX_SOURCE_GITREPO_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_GITREPO_NAME)*
	curl -s --fail https://raw.githubusercontent.com/fluxcd/source-controller/$(FLUX_SOURCE_VERSION)/config/crd/bases/source.toolkit.fluxcd.io_gitrepositories.yaml > $(FLUX_SOURCE_GITREPO_CRD)

$(FLUX_SOURCE_BUCKET_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_BUCKET_NAME)*
	curl -s --fail https://raw.githubusercontent.com/fluxcd/source-controller/$(FLUX_SOURCE_VERSION)/config/crd/bases/source.toolkit.fluxcd.io_buckets.yaml > $(FLUX_SOURCE_BUCKET_CRD)

$(FLUX_SOURCE_OCIREPO_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_OCIREPO_NAME)*
	curl -s --fail https://raw.githubusercontent.com/fluxcd/source-controller/$(FLUX_SOURCE_VERSION)/config/crd/bases/source.toolkit.fluxcd.io_ocirepositories.yaml > $(FLUX_SOURCE_OCIREPO_CRD)

$(FLUX_SOURCE_REPO_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_REPO_NAME)*
	curl -s --fail https://raw.githubusercontent.com/fluxcd/source-controller/$(FLUX_SOURCE_VERSION)/config/crd/bases/source.toolkit.fluxcd.io_helmrepositories.yaml > $(FLUX_SOURCE_REPO_CRD)

$(VELERO_BACKUP_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(VELERO_BACKUP_NAME)*
	curl -s --fail https://raw.githubusercontent.com/vmware-tanzu/velero/$(VELERO_VERSION)/config/crd/v1/bases/velero.io_backups.yaml > $(VELERO_BACKUP_CRD)

$(SVELTOS_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(SVELTOS_NAME)*
	curl -s --fail https://raw.githubusercontent.com/projectsveltos/sveltos/$(SVELTOS_VERSION)/manifest/crds/sveltos_crds.yaml > $(SVELTOS_CRD)

$(IPAM_INCLUSTER_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(IPAM_INCLUSTER_NAME)*
	curl -s --fail https://raw.githubusercontent.com/kubernetes-sigs/cluster-api-ipam-provider-in-cluster/$(IPAM_INCLUSTER_VERSION)/config/crd/bases/ipam.cluster.x-k8s.io_inclusterippools.yaml > $(IPAM_INCLUSTER_CRD)

capi-operator-crds: CAPI_OPERATOR_CRD_PREFIX="operator.cluster.x-k8s.io_"
capi-operator-crds: | $(EXTERNAL_CRD_DIR) yq
	$(eval CAPI_OPERATOR_VERSION := v$(shell $(YQ) -r '.dependencies.[] | select(.name == "cluster-api-operator") | .version' $(PROVIDER_TEMPLATES_DIR)/kcm-regional/Chart.yaml))
	rm -f $(EXTERNAL_CRD_DIR)/$(CAPI_OPERATOR_CRD_PREFIX)*
	@$(foreach name, \
		addonproviders bootstrapproviders controlplaneproviders coreproviders infrastructureproviders ipamproviders runtimeextensionproviders, \
		curl -s --fail https://raw.githubusercontent.com/kubernetes-sigs/cluster-api-operator/$(CAPI_OPERATOR_VERSION)/config/crd/bases/$(CAPI_OPERATOR_CRD_PREFIX)${name}.yaml \
		> $(EXTERNAL_CRD_DIR)/$(CAPI_OPERATOR_CRD_PREFIX)${name}-$(CAPI_OPERATOR_VERSION).yaml;)

cluster-api-crds: CLUSTER_API_VERSION=$(shell go mod edit -json | jq -r '.Require[] | select(.Path == "sigs.k8s.io/cluster-api") | .Version')
cluster-api-crds: CLUSTER_API_CRD_PREFIX="*cluster.x-k8s.io_"
cluster-api-crds: | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(CLUSTER_API_CRD_PREFIX)*
	@$(foreach name, \
		cluster.x-k8s.io_clusters cluster.x-k8s.io_machinedeployments ipam.cluster.x-k8s.io_ipaddressclaims, \
		curl -s --fail https://raw.githubusercontent.com/kubernetes-sigs/cluster-api/$(CLUSTER_API_VERSION)/config/crd/bases/${name}.yaml \
		> $(EXTERNAL_CRD_DIR)/${name}-$(CLUSTER_API_VERSION).yaml;)

.PHONY: external-crd
external-crd: $(FLUX_HELM_CRD) $(FLUX_SOURCE_CHART_CRD) $(FLUX_SOURCE_REPO_CRD) $(FLUX_SOURCE_GITREPO_CRD) $(FLUX_SOURCE_BUCKET_CRD) $(FLUX_SOURCE_OCIREPO_CRD) $(VELERO_BACKUP_CRD) $(SVELTOS_CRD) $(IPAM_INCLUSTER_CRD) capi-operator-crds cluster-api-crds ## Download all external CRD dependencies.

## Tool Binaries
KUBECTL ?= kubectl
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)
ENVTEST ?= $(LOCALBIN)/setup-envtest-$(ENVTEST_VERSION)
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
HELM ?= $(LOCALBIN)/helm-$(HELM_VERSION)
KIND ?= $(LOCALBIN)/kind-$(KIND_VERSION)
YQ ?= $(LOCALBIN)/yq-$(YQ_VERSION)
CLUSTERAWSADM ?= $(LOCALBIN)/clusterawsadm-$(CLUSTERAWSADM_VERSION)
CLUSTERCTL ?= $(LOCALBIN)/clusterctl-$(CLUSTERCTL_VERSION)
CLOUDNUKE ?= $(LOCALBIN)/cloud-nuke-$(CLOUDNUKE_VERSION)
AZURENUKE ?= $(LOCALBIN)/azure-nuke-$(AZURENUKE_VERSION)
ADDLICENSE ?= $(LOCALBIN)/addlicense-$(ADDLICENSE_VERSION)
ENVSUBST ?= $(LOCALBIN)/envsubst-$(ENVSUBST_VERSION)
AWSCLI ?= $(LOCALBIN)/aws-$(AWSCLI_VERSION)
SUPPORT_BUNDLE_CLI ?= $(LOCALBIN)/support-bundle-$(SUPPORT_BUNDLE_CLI_VERSION)

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.17.2
#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')
GOLANGCI_LINT_VERSION ?= v2.10.1
GOLANGCI_LINT_TIMEOUT ?= 1m
HELM_VERSION ?= v3.18.3
KIND_VERSION ?= v0.31.0
YQ_VERSION ?= v4.45.1
CLOUDNUKE_VERSION = v0.38.2
AZURENUKE_VERSION = v1.2.0
CLUSTERAWSADM_VERSION ?= v2.7.1
CLUSTERCTL_VERSION ?= v1.11.2
ADDLICENSE_VERSION ?= v1.1.1
ENVSUBST_VERSION ?= v1.4.2
AWSCLI_VERSION ?= 2.17.42
SUPPORT_BUNDLE_CLI_VERSION ?= v0.117.0

.PHONY: cli-install
cli-install: controller-gen envtest golangci-lint helm kind yq cloud-nuke azure-nuke clusterawsadm clusterctl addlicense envsubst awscli ## Install all required CLI tools.

.PHONY: helm-plugin-schema
helm-plugin-schema: HELM_SCHEMA_PLUGIN_URL ?= https://github.com/losisin/helm-values-schema-json.git
helm-plugin-schema: HELM_SCHEMA_PLUGIN_VERSION ?= 2.3.1
helm-plugin-schema: HELM_SCHEMA_PLUGIN_NAME ?= schema
helm-plugin-schema: helm ## Install/update Helm schema plugin.
	@set -e; \
	name="$(HELM_SCHEMA_PLUGIN_NAME)"; \
	desired="$(HELM_SCHEMA_PLUGIN_VERSION)"; \
	current="$$( $(HELM) plugin list 2>/dev/null | awk -v n="$$name" '$$1==n{print $$2}' )"; \
	if [ "$$current" != "$$desired" ]; then \
		$(HELM) plugin uninstall "$$name" >/dev/null 2>&1 || true; \
		$(HELM) plugin install "$(HELM_SCHEMA_PLUGIN_URL)" --version "$$desired" >/dev/null; \
	fi

.PHONY: helm-plugin-unittest
helm-plugin-unittest: HELM_UNITTEST_PLUGIN_URL ?= https://github.com/helm-unittest/helm-unittest.git
helm-plugin-unittest: HELM_UNITTEST_PLUGIN_VERSION ?= 1.1.0
helm-plugin-unittest: HELM_UNITTEST_PLUGIN_NAME ?= unittest
helm-plugin-unittest: helm ## Install/update Helm unittest plugin.
	@set -e; \
	name="$(HELM_UNITTEST_PLUGIN_NAME)"; \
	desired="$(HELM_UNITTEST_PLUGIN_VERSION)"; \
	current="$$( $(HELM) plugin list 2>/dev/null | awk -v n="$$name" '$$1==n{print $$2}' )"; \
	if [ "$${current#v}" != "$$desired" ]; then \
		$(HELM) plugin uninstall "$$name" >/dev/null 2>&1 || true; \
		$(HELM) plugin install "$(HELM_UNITTEST_PLUGIN_URL)" --version "v$$desired" >/dev/null; \
	fi

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): | $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): | $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): | $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,${GOLANGCI_LINT_VERSION})

.PHONY: helm
helm: $(HELM) ## Download Helm locally if necessary.
$(HELM): HELM_INSTALL_SCRIPT="https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3"
$(HELM): | $(LOCALBIN)
	rm -f $(LOCALBIN)/helm-*
	curl -s --fail $(HELM_INSTALL_SCRIPT) | USE_SUDO=false HELM_INSTALL_DIR=$(LOCALBIN) DESIRED_VERSION=$(HELM_VERSION) BINARY_NAME=helm-$(HELM_VERSION) PATH="$(LOCALBIN):$(PATH)" $(SHELL)

.PHONY: kind
kind: $(KIND) ## Download kind locally if necessary.
$(KIND): | $(LOCALBIN)
	$(call go-install-tool,$(KIND),sigs.k8s.io/kind,${KIND_VERSION})

.PHONY: yq
yq: $(YQ) ## Download yq locally if necessary.
$(YQ): | $(LOCALBIN)
	$(call go-install-tool,$(YQ),github.com/mikefarah/yq/v4,${YQ_VERSION})

.PHONY: cloud-nuke
cloud-nuke: $(CLOUDNUKE) ## Download cloud-nuke locally if necessary.
$(CLOUDNUKE): | $(LOCALBIN)
	$(call go-install-tool,$(CLOUDNUKE),github.com/gruntwork-io/cloud-nuke,$(CLOUDNUKE_VERSION))

.PHONY: azure-nuke
azure-nuke: $(AZURENUKE) ## Download azure-nuke locally if necessary.
$(AZURENUKE): | $(LOCALBIN)
	$(call go-install-tool,$(AZURENUKE),github.com/ekristen/azure-nuke,$(AZURENUKE_VERSION))

.PHONY: clusterawsadm
clusterawsadm: $(CLUSTERAWSADM) ## Download clusterawsadm locally if necessary.
$(CLUSTERAWSADM): | $(LOCALBIN)
	curl -sL --fail https://github.com/kubernetes-sigs/cluster-api-provider-aws/releases/download/$(CLUSTERAWSADM_VERSION)/clusterawsadm-$(HOSTOS)-$(HOSTARCH) -o $(CLUSTERAWSADM) && \
	chmod +x $(CLUSTERAWSADM)

.PHONY: clusterctl
clusterctl: $(CLUSTERCTL) ## Download clusterctl locally if necessary.
$(CLUSTERCTL): | $(LOCALBIN)
	$(call go-install-tool,$(CLUSTERCTL),sigs.k8s.io/cluster-api/cmd/clusterctl,$(CLUSTERCTL_VERSION))

.PHONY: addlicense
addlicense: $(ADDLICENSE) ## Download addlicense locally if necessary.
$(ADDLICENSE): | $(LOCALBIN)
	$(call go-install-tool,$(ADDLICENSE),github.com/google/addlicense,${ADDLICENSE_VERSION})

.PHONY: envsubst
envsubst: $(ENVSUBST) ## Download envsubst locally if necessary.
$(ENVSUBST): | $(LOCALBIN)
	$(call go-install-tool,$(ENVSUBST),github.com/a8m/envsubst/cmd/envsubst,${ENVSUBST_VERSION})

.PHONY: awscli
awscli: $(AWSCLI) ## Install AWS CLI into LOCALBIN.
$(AWSCLI): | $(LOCALBIN)
	@HOSTOS=$(HOSTOS) LOCALBIN=$(LOCALBIN) AWSCLI=$(AWSCLI) AWSCLI_VERSION=$(AWSCLI_VERSION) CURDIR=$(CURDIR) ENVSUBST=$(ENVSUBST) $(SHELL) hack/awscli-install.bash

.PHONY: support-bundle-cli
support-bundle-cli: $(SUPPORT_BUNDLE_CLI) ## Download support-bundle CLI locally if necessary.
$(SUPPORT_BUNDLE_CLI): | $(LOCALBIN)
	curl -sL --fail https://github.com/replicatedhq/troubleshoot/releases/download/$(SUPPORT_BUNDLE_CLI_VERSION)/support-bundle_$(HOSTOS)_$(HOSTARCH).tar.gz | tar -xz -C $(LOCALBIN) && \
	mv $(LOCALBIN)/support-bundle $(SUPPORT_BUNDLE_CLI) && \
	chmod +x $(SUPPORT_BUNDLE_CLI)

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary (ideally with version)
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
if [ ! -f $(1) ]; then mv -f "$$(echo "$(1)" | sed "s/-$(3)$$//")" $(1); fi ;\
}
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
