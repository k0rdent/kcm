NAMESPACE ?= hmc-system
VERSION ?= $(shell git describe --tags --always)
VERSION := $(patsubst v%,%,$(VERSION))
FQDN_VERSION = $(subst .,-,$(VERSION))
# Image URL to use all building/pushing image targets
IMG ?= localhost/hmc/controller:latest
IMG_REPO = $(shell echo $(IMG) | cut -d: -f1)
IMG_TAG = $(shell echo $(IMG) | cut -d: -f2)
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.29.0

HOSTOS := $(shell go env GOHOSTOS)
HOSTARCH := $(shell go env GOHOSTARCH)

# aws default values
AWS_REGION ?= us-east-2

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

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
manifests: controller-gen ## Generate CustomResourceDefinition objects.
	$(CONTROLLER_GEN) crd paths="./..." output:crd:artifacts:config=$(PROVIDER_TEMPLATES_DIR)/hmc/templates/crds

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: set-hmc-version
set-hmc-version: yq
	$(YQ) eval '.version = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/hmc/Chart.yaml
	$(YQ) eval '.version = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/hmc-templates/Chart.yaml
	$(YQ) eval '.image.tag = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/hmc/values.yaml
	$(YQ) eval '.spec.version = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/hmc-templates/files/release.yaml
	$(YQ) eval '.metadata.name = "hmc-$(FQDN_VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/hmc-templates/files/release.yaml
	$(YQ) eval '.spec.hmc.template = "hmc-$(FQDN_VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/hmc-templates/files/release.yaml

.PHONY: hmc-chart-release
hmc-chart-release: set-hmc-version templates-generate ## Generate hmc helm chart

.PHONY: hmc-dist-release
hmc-dist-release: $(HELM) $(YQ)
	@mkdir -p dist
	@printf "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: $(NAMESPACE)\n" > dist/install.yaml
	$(HELM) template -n $(NAMESPACE) hmc $(PROVIDER_TEMPLATES_DIR)/hmc >> dist/install.yaml
	$(YQ) eval -i '.metadata.namespace = "hmc-system"' dist/install.yaml
	$(YQ) eval -i '.metadata.annotations."meta.helm.sh/release-name" = "hmc"' dist/install.yaml
	$(YQ) eval -i '.metadata.annotations."meta.helm.sh/release-namespace" = "hmc-system"' dist/install.yaml
	$(YQ) eval -i '.metadata.labels."app.kubernetes.io/managed-by" = "Helm"' dist/install.yaml

.PHONY: templates-generate
templates-generate:
	@hack/templates.sh

.PHONY: generate-all
generate-all: generate manifests templates-generate add-license

.PHONY: fmt
fmt: ## Run 'go fmt' against code.
	go fmt ./...

.PHONY: vet
vet: ## Run 'go vet' against code.
	go vet ./...

.PHONY: tidy
tidy: ## Run 'go mod tidy' against code.
	go mod tidy

.PHONY: test
test: generate-all envtest tidy external-crd ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# Utilize Kind or modify the e2e tests to load the image locally, enabling
# compatibility with other vendors.
.PHONY: test-e2e
test-e2e: cli-install ## Run the e2e tests using a Kind k8s instance as the management cluster.
	@if [ "$$GINKGO_LABEL_FILTER" ]; then \
		ginkgo_label_flag="-ginkgo.label-filter=$$GINKGO_LABEL_FILTER"; \
	fi; \
	KIND_CLUSTER_NAME="hmc-test" KIND_VERSION=$(KIND_VERSION) go test ./test/e2e/ -v -ginkgo.v -ginkgo.timeout=3h -timeout=3h $$ginkgo_label_flag

.PHONY: lint
lint: golangci-lint fmt vet ## Run golangci-lint linter & yamllint
	@$(GOLANGCI_LINT) run --timeout=$(GOLANGCI_LINT_TIMEOUT)

.PHONY: lint-fix
lint-fix: golangci-lint fmt vet ## Run golangci-lint linter and perform fixes
	@$(GOLANGCI_LINT) run --fix

.PHONY: add-license
add-license: addlicense
	$(ADDLICENSE) -c "" -ignore ".github/**" -ignore "config/**" -ignore "templates/**" -ignore ".*" -y 2024 .

##@ Package

TEMPLATES_DIR := templates
PROVIDER_TEMPLATES_DIR := $(TEMPLATES_DIR)/provider
export PROVIDER_TEMPLATES_DIR
CLUSTER_TEMPLATES_DIR := $(TEMPLATES_DIR)/cluster
CHARTS_PACKAGE_DIR ?= $(LOCALBIN)/charts
EXTENSION_CHARTS_PACKAGE_DIR ?= $(LOCALBIN)/charts/extensions
K0S_VERSION = $(shell $(YQ) '.k0s.version' $(CLUSTER_TEMPLATES_DIR)/vsphere-standalone-cp/values.yaml)
K0S_AG_IMAGE = k0s-ag-image:$(subst +,-,$(K0S_VERSION))
$(EXTENSION_CHARTS_PACKAGE_DIR): | $(LOCALBIN)
	mkdir -p $(EXTENSION_CHARTS_PACKAGE_DIR)
$(CHARTS_PACKAGE_DIR): | $(LOCALBIN)
	rm -rf $(CHARTS_PACKAGE_DIR)
	mkdir -p $(CHARTS_PACKAGE_DIR)
IMAGES_PACKAGE_DIR ?= $(LOCALBIN)/images
$(IMAGES_PACKAGE_DIR): | $(LOCALBIN)
	rm -rf $(IMAGES_PACKAGE_DIR)
	mkdir -p $(IMAGES_PACKAGE_DIR)

TEMPLATE_FOLDERS = $(patsubst $(TEMPLATES_DIR)/%,%,$(wildcard $(TEMPLATES_DIR)/*))

.PHONY: collect-airgap-providers
collect-airgap-providers: yq helm clusterctl $(PROVIDER_TEMPLATES_DIR) $(LOCALBIN)
	$(SHELL) hack/collect-airgap-providers.sh

.PHONY: helm-package
helm-package: $(CHARTS_PACKAGE_DIR) $(EXTENSION_CHARTS_PACKAGE_DIR) helm collect-airgap-providers
	@make $(patsubst %,package-%-tmpl,$(TEMPLATE_FOLDERS))

.PHONY: k0s-image
k0s-image:
	export DOCKER_BUILDKIT=1
	$(CONTAINER_TOOL) build --build-arg K0S_VERSION=$(K0S_VERSION) -t $(K0S_AG_IMAGE) hack/k0s-ag-image

bundle-images: dev-apply $(IMAGES_PACKAGE_DIR) k0s-image ## Create a tarball with all images used by HMC.
	@BUNDLE_TARBALL=$(IMAGES_PACKAGE_DIR)/hmc-images-$(VERSION).tgz EXTENSIONS_BUNDLE_TARBALL=$(IMAGES_PACKAGE_DIR)/hmc-extension-images-$(VERSION).tgz IMG=$(IMG) KUBECTL=$(KUBECTL) YQ=$(YQ) HELM=$(HELM) NAMESPACE=$(NAMESPACE) TEMPLATES_DIR=$(TEMPLATES_DIR) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) K0S_AG_IMAGE=$(K0S_AG_IMAGE) $(SHELL) $(CURDIR)/scripts/bundle-images.sh

airgap-package: bundle-images ## Create a tarball with all images and Helm charts used by HMC, useful for deploying in air-gapped environments.
	@TEMPLATES_DIR=$(TEMPLATES_DIR) EXTENSION_CHARTS_PACKAGE_DIR=$(EXTENSION_CHARTS_PACKAGE_DIR) HELM=$(HELM) YQ=$(YQ) $(SHELL) $(CURDIR)/scripts/package-k0s-extensions-helm.sh
	cd $(LOCALBIN) && mkdir -p scripts && cp ../scripts/airgap-push.sh scripts/airgap-push.sh && tar -czf hmc-airgap-$(VERSION).tgz scripts/airgap-push.sh $(shell basename $(CHARTS_PACKAGE_DIR)) $(shell basename $(IMAGES_PACKAGE_DIR))

package-%-tmpl:
	@make TEMPLATES_SUBDIR=$(TEMPLATES_DIR)/$* $(patsubst %,package-chart-%,$(shell ls $(TEMPLATES_DIR)/$*))

lint-chart-%:
	$(HELM) dependency update $(TEMPLATES_SUBDIR)/$*
	$(HELM) lint --strict $(TEMPLATES_SUBDIR)/$*

package-chart-%: lint-chart-%
	$(HELM) package --destination $(CHARTS_PACKAGE_DIR) $(TEMPLATES_SUBDIR)/$*

##@ Build

LD_FLAGS?= -s -w
LD_FLAGS += -X github.com/K0rdent/kcm/internal/build.Version=$(VERSION)
LD_FLAGS += -X github.com/K0rdent/kcm/internal/telemetry.segmentToken=$(SEGMENT_TOKEN)

.PHONY: build
build: generate-all ## Build manager binary.
	go build -ldflags="${LD_FLAGS}" -o bin/manager cmd/main.go

.PHONY: run
run: generate-all ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build \
	-t ${IMG} \
	--build-arg LD_FLAGS="${LD_FLAGS}" \
	.

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
        # copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name project-v3-builder
	$(CONTAINER_TOOL) buildx use project-v3-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm project-v3-builder
	rm Dockerfile.cross

##@ Deployment

KIND_CLUSTER_NAME ?= hmc-dev
KIND_NETWORK ?= kind
REGISTRY_NAME ?= hmc-local-registry
REGISTRY_PORT ?= 5001
REGISTRY_REPO ?= oci://127.0.0.1:$(REGISTRY_PORT)/charts
DEV_PROVIDER ?= aws
REGISTRY_IS_OCI = $(shell echo $(REGISTRY_REPO) | grep -q oci && echo true || echo false)
AWS_CREDENTIALS=${AWS_B64ENCODED_CREDENTIALS}

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: kind-deploy
kind-deploy: kind
	@if ! $(KIND) get clusters | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		$(KIND) create cluster -n $(KIND_CLUSTER_NAME); \
	fi

.PHONY: kind-undeploy
kind-undeploy: kind
	@if $(KIND) get clusters | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		$(KIND) delete cluster --name $(KIND_CLUSTER_NAME); \
	fi

.PHONY: registry-deploy
registry-deploy:
	@if [ ! "$$($(CONTAINER_TOOL) ps -aq -f name=$(REGISTRY_NAME))" ]; then \
		echo "Starting new local registry container $(REGISTRY_NAME)"; \
		$(CONTAINER_TOOL) run -d --restart=always -p "127.0.0.1:$(REGISTRY_PORT):5000" --network bridge --name "$(REGISTRY_NAME)" registry:2; \
	fi; \
	if [ "$$($(CONTAINER_TOOL) inspect -f='{{json .NetworkSettings.Networks.$(KIND_NETWORK)}}' $(REGISTRY_NAME))" = 'null' ]; then \
		$(CONTAINER_TOOL) network connect $(KIND_NETWORK) $(REGISTRY_NAME); \
	fi

.PHONY: registry-undeploy
registry-undeploy:
	@if [ "$$($(CONTAINER_TOOL) ps -aq -f name=$(REGISTRY_NAME))" ]; then \
		echo "Removing local registry container $(REGISTRY_NAME)"; \
		$(CONTAINER_TOOL) rm -f "$(REGISTRY_NAME)"; \
	fi

.PHONY: hmc-deploy
hmc-deploy: helm
	$(HELM) upgrade --values $(HMC_VALUES) --reuse-values --install --create-namespace hmc $(PROVIDER_TEMPLATES_DIR)/hmc -n $(NAMESPACE)

.PHONY: dev-deploy
dev-deploy: ## Deploy HMC helm chart to the K8s cluster specified in ~/.kube/config.
	@$(YQ) eval -i '.image.repository = "$(IMG_REPO)"' config/dev/hmc_values.yaml
	@$(YQ) eval -i '.image.tag = "$(IMG_TAG)"' config/dev/hmc_values.yaml
	@if [ "$(REGISTRY_REPO)" = "oci://127.0.0.1:$(REGISTRY_PORT)/charts" ]; then \
		$(YQ) eval -i '.controller.defaultRegistryURL = "oci://$(REGISTRY_NAME):5000/charts"' config/dev/hmc_values.yaml; \
	else \
		$(YQ) eval -i '.controller.defaultRegistryURL = "$(REGISTRY_REPO)"' config/dev/hmc_values.yaml; \
	fi; \
	$(MAKE) hmc-deploy HMC_VALUES=config/dev/hmc_values.yaml
	$(KUBECTL) rollout restart -n $(NAMESPACE) deployment/hmc-controller-manager

.PHONY: dev-undeploy
dev-undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	$(HELM) delete -n $(NAMESPACE) hmc

.PHONY: helm-push
helm-push: helm-package
	@if [ ! $(REGISTRY_IS_OCI) ]; then \
	    repo_flag="--repo"; \
	fi; \
	for chart in $(CHARTS_PACKAGE_DIR)/*.tgz; do \
		base=$$(basename $$chart .tgz); \
		chart_version=$$(echo $$base | grep -o "v\{0,1\}[0-9]\+\.[0-9]\+\.[0-9].*"); \
		chart_name="$${base%-"$$chart_version"}"; \
		echo "Verifying if chart $$chart_name, version $$chart_version already exists in $(REGISTRY_REPO)"; \
		if $(REGISTRY_IS_OCI); then \
			chart_exists=$$($(HELM) pull $$repo_flag $(REGISTRY_REPO)/$$chart_name --version $$chart_version --destination /tmp 2>&1 | grep "not found" || true); \
		else \
			chart_exists=$$($(HELM) pull $$repo_flag $(REGISTRY_REPO) $$chart_name --version $$chart_version --destination /tmp 2>&1 | grep "not found" || true); \
		fi; \
		if [ -z "$$chart_exists" ]; then \
			echo "Chart $$chart_name version $$chart_version already exists in the repository."; \
		else \
			if $(REGISTRY_IS_OCI); then \
				echo "Pushing $$chart to $(REGISTRY_REPO)"; \
				$(HELM) push "$$chart" $(REGISTRY_REPO); \
			else \
				if [ ! $$REGISTRY_USERNAME ] && [ ! $$REGISTRY_PASSWORD ]; then \
					echo "REGISTRY_USERNAME and REGISTRY_PASSWORD must be populated to push the chart to an HTTPS repository"; \
					exit 1; \
				else \
					$(HELM) repo add hmc $(REGISTRY_REPO); \
					echo "Pushing $$chart to $(REGISTRY_REPO)"; \
					$(HELM) cm-push "$$chart" $(REGISTRY_REPO) --username $$REGISTRY_USERNAME --password $$REGISTRY_PASSWORD; \
				fi; \
			fi; \
		fi; \
	done

# kind doesn't support load docker-image if the container tool is podman
# https://github.com/kubernetes-sigs/kind/issues/2038
.PHONY: dev-push
dev-push: docker-build helm-push
	@if [ "$(CONTAINER_TOOL)" = "podman" ]; then \
		$(KIND) load image-archive --name $(KIND_CLUSTER_NAME) <($(CONTAINER_TOOL) save $(IMG)); \
	else \
		$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER_NAME); \
	fi; \

.PHONY: dev-templates
dev-templates: templates-generate
	$(KUBECTL) -n $(NAMESPACE) apply --force -f $(PROVIDER_TEMPLATES_DIR)/hmc-templates/files/templates

.PHONY: dev-release
dev-release:
	@$(YQ) e ".spec.version = \"${VERSION}\"" $(PROVIDER_TEMPLATES_DIR)/hmc-templates/files/release.yaml | $(KUBECTL) -n $(NAMESPACE) apply -f -

.PHONY: dev-adopted-creds
dev-adopted-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -i config/dev/adopted-credentials.yaml | $(KUBECTL) apply -f -

.PHONY: dev-aws-creds
dev-aws-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -i config/dev/aws-credentials.yaml | $(KUBECTL) apply -f -

.PHONY: dev-azure-creds
dev-azure-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/azure-credentials.yaml | $(KUBECTL) apply -f -

.PHONY: dev-vsphere-creds
dev-vsphere-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/vsphere-credentials.yaml | $(KUBECTL) apply -f -

dev-eks-creds: dev-aws-creds

.PHONY: dev-aks-creds
dev-aks-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/aks-credentials.yaml | $(KUBECTL) apply -f -

.PHONY: dev-openstack-creds
dev-openstack-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/openstack-credentials.yaml | $(KUBECTL) apply -f -

.PHONY: dev-apply
dev-apply: kind-deploy registry-deploy dev-push dev-deploy dev-templates dev-release ## Apply the development environment by deploying the kind cluster, local registry and the HMC helm chart.

.PHONY: test-apply
test-apply: set-hmc-version helm-package dev-deploy dev-templates dev-release

.PHONY: dev-destroy
dev-destroy: kind-undeploy registry-undeploy ## Destroy the development environment by deleting the kind cluster and local registry.

.PHONY: dev-mcluster-apply
dev-mcluster-apply: envsubst ## Create dev managed cluster using 'config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml'
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml | $(KUBECTL) apply -f -

.PHONY: dev-mcluster-delete
dev-mcluster-delete: envsubst ## Delete dev managed cluster using 'config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml'
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml | $(KUBECTL) delete -f -

.PHONY: dev-creds-apply
dev-creds-apply: dev-$(DEV_PROVIDER)-creds ## Create credentials resources for $DEV_PROVIDER

.PHONY: dev-aws-nuke
dev-aws-nuke: envsubst awscli yq cloud-nuke ## Warning: Destructive! Nuke all AWS resources deployed by 'DEV_PROVIDER=aws dev-mcluster-apply'
	@CLUSTER_NAME=$(CLUSTER_NAME) YQ=$(YQ) AWSCLI=$(AWSCLI) $(SHELL) $(CURDIR)/scripts/aws-nuke-ccm.sh elb
	@CLUSTER_NAME=$(CLUSTER_NAME) $(ENVSUBST) < config/dev/aws-cloud-nuke.yaml.tpl > config/dev/aws-cloud-nuke.yaml
	DISABLE_TELEMETRY=true $(CLOUDNUKE) aws --region $$AWS_REGION --force --config config/dev/aws-cloud-nuke.yaml --resource-type vpc,eip,nat-gateway,ec2,ec2-subnet,elb,elbv2,ebs,internet-gateway,network-interface,security-group
	@rm config/dev/aws-cloud-nuke.yaml
	@CLUSTER_NAME=$(CLUSTER_NAME) YQ=$(YQ) AWSCLI=$(AWSCLI) $(SHELL) $(CURDIR)/scripts/aws-nuke-ccm.sh ebs

.PHONY: dev-azure-nuke
dev-azure-nuke: envsubst azure-nuke ## Warning: Destructive! Nuke all Azure resources deployed by 'DEV_PROVIDER=azure dev-mcluster-apply'
	@if [ "$(CLUSTER_NAME)" == "" ] || [ "$(AZURE_TENANT_ID)" == "" ] || [ "$(AZURE_REGION)" == "" ]; then \
		echo "CLUSTER_NAME, AZURE_TENANT_ID and AZURE_REGION must be set"; \
		exit 1; \
	fi
	@CLUSTER_NAME=$(CLUSTER_NAME) $(ENVSUBST) < config/dev/azure-cloud-nuke.yaml.tpl > config/dev/azure-cloud-nuke.yaml
	$(AZURENUKE) run --config config/dev/azure-cloud-nuke.yaml --force --no-dry-run
	@rm config/dev/azure-cloud-nuke.yaml

.PHONY: cli-install
cli-install: clusterawsadm clusterctl cloud-nuke envsubst yq awscli ## Install the necessary CLI tools for deployment, development and testing.

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
export LOCALBIN
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

EXTERNAL_CRD_DIR ?= $(LOCALBIN)/crd
$(EXTERNAL_CRD_DIR): $(LOCALBIN)
	mkdir -p $(EXTERNAL_CRD_DIR)

FLUX_SOURCE_VERSION ?= $(shell go mod edit -json | jq -r '.Require[] | select(.Path == "github.com/fluxcd/source-controller/api") | .Version')
FLUX_SOURCE_REPO_NAME ?= source-helmrepositories
FLUX_SOURCE_REPO_CRD ?= $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_REPO_NAME)-$(FLUX_SOURCE_VERSION).yaml
FLUX_SOURCE_CHART_NAME ?= source-helmchart
FLUX_SOURCE_CHART_CRD ?= $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_CHART_NAME)-$(FLUX_SOURCE_VERSION).yaml

FLUX_HELM_VERSION ?= $(shell go mod edit -json | jq -r '.Require[] | select(.Path == "github.com/fluxcd/helm-controller/api") | .Version')
FLUX_HELM_NAME ?= helm
FLUX_HELM_CRD ?= $(EXTERNAL_CRD_DIR)/$(FLUX_HELM_NAME)-$(FLUX_HELM_VERSION).yaml

SVELTOS_VERSION ?= v$(shell $(YQ) -r '.appVersion' $(PROVIDER_TEMPLATES_DIR)/projectsveltos/Chart.yaml)
SVELTOS_NAME ?= sveltos
SVELTOS_CRD ?= $(EXTERNAL_CRD_DIR)/$(SVELTOS_NAME)-$(SVELTOS_VERSION).yaml

CAPI_OPERATOR_VERSION ?= v$(shell $(YQ) -r '.dependencies.[] | select(.name == "cluster-api-operator") | .version' $(PROVIDER_TEMPLATES_DIR)/hmc/Chart.yaml)
CAPI_OPERATOR_CRD_PREFIX ?= "operator.cluster.x-k8s.io_"
CAPI_OPERATOR_CRDS ?= capi-operator-crds

CLUSTER_API_VERSION ?= v1.9.3
CLUSTER_API_CRD_PREFIX ?= "cluster.x-k8s.io_"
CLUSTER_API_CRDS ?= cluster-api-crds

## Tool Binaries
KUBECTL ?= kubectl
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)
ENVTEST ?= $(LOCALBIN)/setup-envtest-$(ENVTEST_VERSION)
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
HELM ?= $(LOCALBIN)/helm-$(HELM_VERSION)
export HELM
KIND ?= $(LOCALBIN)/kind-$(KIND_VERSION)
YQ ?= $(LOCALBIN)/yq-$(YQ_VERSION)
export YQ
CLUSTERAWSADM ?= $(LOCALBIN)/clusterawsadm
CLUSTERCTL ?= $(LOCALBIN)/clusterctl
export CLUSTERCTL
CLOUDNUKE ?= $(LOCALBIN)/cloud-nuke
AZURENUKE ?= $(LOCALBIN)/azure-nuke
ADDLICENSE ?= $(LOCALBIN)/addlicense-$(ADDLICENSE_VERSION)
ENVSUBST ?= $(LOCALBIN)/envsubst-$(ENVSUBST_VERSION)
AWSCLI ?= $(LOCALBIN)/aws

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.16.3
ENVTEST_VERSION ?= release-0.17
GOLANGCI_LINT_VERSION ?= v1.61.0
GOLANGCI_LINT_TIMEOUT ?= 1m
HELM_VERSION ?= v3.15.1
KIND_VERSION ?= v0.23.0
YQ_VERSION ?= v4.44.2
CLOUDNUKE_VERSION = v0.37.1
AZURENUKE_VERSION = v1.1.0
CLUSTERAWSADM_VERSION ?= v2.5.2
CLUSTERCTL_VERSION ?= v1.8.5
ADDLICENSE_VERSION ?= v1.1.1
ENVSUBST_VERSION ?= v1.4.2
AWSCLI_VERSION ?= 2.17.42

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
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint,${GOLANGCI_LINT_VERSION})

.PHONY: helm
helm: $(HELM) ## Download helm locally if necessary.
HELM_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3"
$(HELM): | $(LOCALBIN)
	rm -f $(LOCALBIN)/helm-*
	curl -s --fail $(HELM_INSTALL_SCRIPT) | USE_SUDO=false HELM_INSTALL_DIR=$(LOCALBIN) DESIRED_VERSION=$(HELM_VERSION) BINARY_NAME=helm-$(HELM_VERSION) PATH="$(LOCALBIN):$(PATH)" bash

$(FLUX_HELM_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(FLUX_HELM_NAME)*
	curl -s --fail https://raw.githubusercontent.com/fluxcd/helm-controller/$(FLUX_HELM_VERSION)/config/crd/bases/helm.toolkit.fluxcd.io_helmreleases.yaml > $(FLUX_HELM_CRD)

$(FLUX_SOURCE_CHART_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_CHART_NAME)*
	curl -s --fail https://raw.githubusercontent.com/fluxcd/source-controller/$(FLUX_SOURCE_VERSION)/config/crd/bases/source.toolkit.fluxcd.io_helmcharts.yaml > $(FLUX_SOURCE_CHART_CRD)

$(FLUX_SOURCE_REPO_CRD): | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(FLUX_SOURCE_REPO_NAME)*
	curl -s --fail https://raw.githubusercontent.com/fluxcd/source-controller/$(FLUX_SOURCE_VERSION)/config/crd/bases/source.toolkit.fluxcd.io_helmrepositories.yaml > $(FLUX_SOURCE_REPO_CRD)

$(SVELTOS_CRD): | yq $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(SVELTOS_NAME)*
	curl -s --fail https://raw.githubusercontent.com/projectsveltos/sveltos/$(SVELTOS_VERSION)/manifest/crds/sveltos_crds.yaml > $(SVELTOS_CRD)

$(CAPI_OPERATOR_CRDS): | $(YQ) $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(CAPI_OPERATOR_CRD_PREFIX)*
	@$(foreach name, \
		addonproviders bootstrapproviders controlplaneproviders coreproviders infrastructureproviders ipamproviders runtimeextensionproviders, \
		curl -s --fail https://raw.githubusercontent.com/kubernetes-sigs/cluster-api-operator/$(CAPI_OPERATOR_VERSION)/config/crd/bases/$(CAPI_OPERATOR_CRD_PREFIX)${name}.yaml \
		> $(EXTERNAL_CRD_DIR)/$(CAPI_OPERATOR_CRD_PREFIX)${name}-$(CAPI_OPERATOR_VERSION).yaml;)

$(CLUSTER_API_CRDS): | $(YQ) $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(CLUSTER_API_CRD_PREFIX)*
	@$(foreach name, \
		clusters machinedeployments, \
		curl -s --fail https://raw.githubusercontent.com/kubernetes-sigs/cluster-api/$(CLUSTER_API_VERSION)/config/crd/bases/$(CLUSTER_API_CRD_PREFIX)${name}.yaml \
		> $(EXTERNAL_CRD_DIR)/$(CLUSTER_API_CRD_PREFIX)${name}-$(CLUSTER_API_VERSION).yaml;)

.PHONY: external-crd
external-crd: $(FLUX_HELM_CRD) $(FLUX_SOURCE_CHART_CRD) $(FLUX_SOURCE_REPO_CRD) $(SVELTOS_CRD) $(CAPI_OPERATOR_CRDS) $(CLUSTER_API_CRDS)

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
	curl -sL --fail https://github.com/gruntwork-io/cloud-nuke/releases/download/$(CLOUDNUKE_VERSION)/cloud-nuke_$(HOSTOS)_$(HOSTARCH) -o $(CLOUDNUKE) && \
	chmod +x $(CLOUDNUKE)

.PHONY: azure-nuke
azure-nuke: $(AZURENUKE) ## Download azure-nuke locally if necessary.
$(AZURENUKE): | $(LOCALBIN)
	curl -sL --fail https://github.com/ekristen/azure-nuke/releases/download/$(AZURENUKE_VERSION)/azure-nuke-$(AZURENUKE_VERSION)-$(HOSTOS)-$(HOSTARCH).tar.gz | \
	tar xvzf - --to-stdout azure-nuke > $(LOCALBIN)/azure-nuke && \
	chmod +x $(LOCALBIN)/azure-nuke

.PHONY: clusterawsadm
clusterawsadm: $(CLUSTERAWSADM) ## Download clusterawsadm locally if necessary.
$(CLUSTERAWSADM): | $(LOCALBIN)
	curl -sL --fail https://github.com/kubernetes-sigs/cluster-api-provider-aws/releases/download/$(CLUSTERAWSADM_VERSION)/clusterawsadm_$(CLUSTERAWSADM_VERSION)_$(HOSTOS)_$(HOSTARCH) -o $(CLUSTERAWSADM) && \
	chmod +x $(CLUSTERAWSADM)

.PHONY: clusterctl
clusterctl: $(CLUSTERCTL) ## Download clusterctl locally if necessary.
$(CLUSTERCTL): | $(LOCALBIN)
	curl -fsL https://github.com/kubernetes-sigs/cluster-api/releases/download/$(CLUSTERCTL_VERSION)/clusterctl-$(HOSTOS)-$(HOSTARCH) -o $(CLUSTERCTL)
	chmod +x $(CLUSTERCTL)

.PHONY: addlicense
addlicense: $(ADDLICENSE) ## Download addlicense locally if necessary.
$(ADDLICENSE): | $(LOCALBIN)
	$(call go-install-tool,$(ADDLICENSE),github.com/google/addlicense,${ADDLICENSE_VERSION})

.PHONY: envsubst
envsubst: $(ENVSUBST)
$(ENVSUBST): | $(LOCALBIN)
	$(call go-install-tool,$(ENVSUBST),github.com/a8m/envsubst/cmd/envsubst,${ENVSUBST_VERSION})

.PHONY: awscli
awscli: $(AWSCLI)
$(AWSCLI): | $(LOCALBIN)
	@if [ $(HOSTOS) == "linux" ]; then \
		curl --fail "https://awscli.amazonaws.com/awscli-exe-linux-$(shell uname -m)-$(AWSCLI_VERSION).zip" -o "/tmp/awscliv2.zip" && \
		unzip -oqq /tmp/awscliv2.zip -d /tmp && \
		/tmp/aws/install -i $(LOCALBIN)/aws-cli -b $(LOCALBIN) --update; \
	fi; \
	if [ $(HOSTOS) == "darwin" ]; then \
		curl --fail "https://awscli.amazonaws.com/AWSCLIV2.pkg" -o "AWSCLIV2.pkg" && \
		LOCALBIN="$(LOCALBIN)" $(ENVSUBST) -i ./scripts/awscli-darwin-install.xml.tpl > choices.xml && \
		sudo installer -pkg AWSCLIV2.pkg -target $(LOCALBIN) -applyChoiceChangesXML ./choices.xml && \
		ln -s $(LOCALBIN)/aws-cli/aws $(LOCALBIN)/aws && \
		rm AWSCLIV2.pkg && rm ./choices.xml; \
	fi; \
	if [ $(HOSTOS) == "windows" ]; then \
		echo "Installing to $(LOCALBIN) on Windows is not yet implemented" && \
		exit 1; \
	fi; \

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
