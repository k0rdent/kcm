NAMESPACE ?= kcm-system
CLUSTER_NAME_SUFFIX ?= dev
VERSION ?= $(shell git describe --tags --always)
VERSION := $(patsubst v%,%,$(VERSION))
FQDN_VERSION = $(subst .,-,$(VERSION))
# Image URL to use all building/pushing image targets
IMG ?= localhost/kcm/controller:latest
IMG_REPO = $(shell echo $(IMG) | cut -d: -f1)
IMG_TAG = $(shell echo $(IMG) | cut -d: -f2)
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.32.0

KCM_STABLE_VERSION = $(shell git ls-remote --tags --sort v:refname --exit-code --refs https://github.com/k0rdent/kcm | tail -n1 | cut -d '/' -f3)

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

TEMPLATES_DIR := templates
PROVIDER_TEMPLATES_DIR := $(TEMPLATES_DIR)/provider
CLUSTER_TEMPLATES_DIR := $(TEMPLATES_DIR)/cluster

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
	$(CONTROLLER_GEN) crd paths="./..." output:crd:artifacts:config=$(PROVIDER_TEMPLATES_DIR)/kcm/templates/crds

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: set-kcm-version
set-kcm-version: yq
	$(YQ) eval '.version = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm/Chart.yaml
	$(YQ) eval '.version = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm-templates/Chart.yaml
	$(YQ) eval '.image.tag = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm/values.yaml
	$(YQ) eval '.spec.version = "$(VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm-templates/files/release.yaml
	$(YQ) eval '.metadata.name = "kcm-$(FQDN_VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm-templates/files/release.yaml
	$(YQ) eval '.spec.kcm.template = "kcm-$(FQDN_VERSION)"' -i $(PROVIDER_TEMPLATES_DIR)/kcm-templates/files/release.yaml

.PHONY: kcm-chart-release
kcm-chart-release: set-kcm-version templates-generate ## Generate kcm helm chart

.PHONY: kcm-dist-release
kcm-dist-release: helm yq
	@mkdir -p dist
	@printf "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: $(NAMESPACE)\n" > dist/install.yaml
	$(HELM) template -n $(NAMESPACE) kcm $(PROVIDER_TEMPLATES_DIR)/kcm >> dist/install.yaml
	$(YQ) eval -i '.metadata.namespace = "kcm-system"' dist/install.yaml
	$(YQ) eval -i '.metadata.annotations."meta.helm.sh/release-name" = "kcm"' dist/install.yaml
	$(YQ) eval -i '.metadata.annotations."meta.helm.sh/release-namespace" = "kcm-system"' dist/install.yaml
	$(YQ) eval -i '.metadata.labels."app.kubernetes.io/managed-by" = "Helm"' dist/install.yaml

.PHONY: templates-generate
templates-generate:
	@hack/templates.sh

.PHONY: capo-orc-fetch
capo-orc-fetch: CAPO_DIR := $(PROVIDER_TEMPLATES_DIR)/cluster-api-provider-openstack
capo-orc-fetch: CAPO_ORC_VERSION ?= $(shell grep 'orcVersion:' $(CAPO_DIR)/values.yaml | cut -d '"' -f 2)
capo-orc-fetch: CAPO_ORC_TEMPLATE := "$(CAPO_DIR)/templates/orc-$(shell echo $(CAPO_ORC_VERSION) | sed 's/\./-/g').yaml"
capo-orc-fetch: yq
	@if ! test -s "$(CAPO_ORC_TEMPLATE)"; then \
	  	curl -L --fail -s https://github.com/k-orc/openstack-resource-controller/releases/download/v$(CAPO_ORC_VERSION)/install.yaml -o $(CAPO_ORC_TEMPLATE); \
	  	$(YQ) -i '(select(.kind == "Deployment") | .spec.template.spec.containers.[0].image) |= sub("$(CAPO_ORC_VERSION)"/"{{ .Values.orcVersion }}")' $(CAPO_ORC_TEMPLATE); \
	  	printf '%s\n' "{{ if (eq .Values.orcVersion \"$(CAPO_ORC_VERSION)\") }}" | cat - $(CAPO_ORC_TEMPLATE) > $(CAPO_ORC_TEMPLATE).tmp; \
	  	mv $(CAPO_ORC_TEMPLATE).tmp $(CAPO_ORC_TEMPLATE); \
	  	echo "{{- end }}" >> $(CAPO_ORC_TEMPLATE); \
	fi

.PHONY: generate-all
generate-all: generate manifests templates-generate add-license capo-orc-fetch

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
	KIND_CLUSTER_NAME="kcm-test" KIND_VERSION=$(KIND_VERSION) VALIDATE_CLUSTER_UPGRADE_PATH=false \
	go test ./test/e2e/ -v -ginkgo.v -ginkgo.timeout=3h -timeout=3h $$ginkgo_label_flag

.PHONY: lint
lint: golangci-lint fmt vet ## Run golangci-lint linter & yamllint
	@$(GOLANGCI_LINT) run --timeout=$(GOLANGCI_LINT_TIMEOUT)

.PHONY: lint-fix
lint-fix: golangci-lint fmt vet ## Run golangci-lint linter and perform fixes
	@$(GOLANGCI_LINT) run --fix

.PHONY: add-license
add-license: addlicense
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

.PHONY: load-providers
load-providers:
	@mkdir -p $(PROVIDER_TEMPLATES_DIR)/kcm/files/providers
	@cp -a providers/*.yml $(PROVIDER_TEMPLATES_DIR)/kcm/files/providers/

.PHONY: helm-package
helm-package: $(CHARTS_PACKAGE_DIR) $(EXTENSION_CHARTS_PACKAGE_DIR) helm load-providers
	@make $(patsubst %,package-%-tmpl,$(TEMPLATE_FOLDERS))

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

KIND_CLUSTER_NAME ?= kcm-dev
KIND_NETWORK ?= kind
REGISTRY_NAME ?= kcm-local-registry
REGISTRY_PORT ?= 5001
REGISTRY_REPO ?= oci://127.0.0.1:$(REGISTRY_PORT)/charts
DEV_PROVIDER ?= aws
VALIDATE_CLUSTER_UPGRADE_PATH ?= true
REGISTRY_IS_OCI = $(shell echo $(REGISTRY_REPO) | grep -q oci && echo true || echo false)
AWS_CREDENTIALS=${AWS_B64ENCODED_CREDENTIALS}

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: kind-deploy
kind-deploy: kind
	@if ! $(KIND) get clusters | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		if [ -n "$(KIND_CONFIG_PATH)" ]; then \
			$(KIND) create cluster -n $(KIND_CLUSTER_NAME) --config "$(KIND_CONFIG_PATH)"; \
		else \
			$(KIND) create cluster -n $(KIND_CLUSTER_NAME); \
		fi \
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

.PHONY: kcm-deploy
kcm-deploy: helm
	$(HELM) upgrade --values $(KCM_VALUES) --reuse-values --install --create-namespace kcm $(PROVIDER_TEMPLATES_DIR)/kcm -n $(NAMESPACE)

.PHONY: dev-deploy
dev-deploy: ## Deploy KCM helm chart to the K8s cluster specified in ~/.kube/config.
	@$(YQ) eval -i '.image.repository = "$(IMG_REPO)"' config/dev/kcm_values.yaml
	@$(YQ) eval -i '.image.tag = "$(IMG_TAG)"' config/dev/kcm_values.yaml
	@if [ "$(REGISTRY_REPO)" = "oci://127.0.0.1:$(REGISTRY_PORT)/charts" ]; then \
		$(YQ) eval -i '.controller.defaultRegistryURL = "oci://$(REGISTRY_NAME):5000/charts"' config/dev/kcm_values.yaml; \
	else \
		$(YQ) eval -i '.controller.defaultRegistryURL = "$(REGISTRY_REPO)"' config/dev/kcm_values.yaml; \
	fi;
	@$(YQ) eval -i '.controller.validateClusterUpgradePath = $(VALIDATE_CLUSTER_UPGRADE_PATH)' config/dev/kcm_values.yaml
	$(MAKE) kcm-deploy KCM_VALUES=config/dev/kcm_values.yaml
	$(KUBECTL) rollout restart -n $(NAMESPACE) deployment/kcm-controller-manager

.PHONY: dev-undeploy
dev-undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	$(HELM) delete -n $(NAMESPACE) kcm

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
					$(HELM) repo add kcm $(REGISTRY_REPO); \
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
	$(KUBECTL) -n $(NAMESPACE) apply --force -f $(PROVIDER_TEMPLATES_DIR)/kcm-templates/files/templates

KCM_REPO_URL ?= oci://ghcr.io/k0rdent/kcm/charts
KCM_REPO_NAME ?= kcm

.PHONY: stable-templates
stable-templates: yq
	@printf "%s\n" \
		"apiVersion: source.toolkit.fluxcd.io/v1" \
		"kind: HelmRepository" \
		"metadata:" \
		"  name: $(KCM_REPO_NAME)" \
		"  labels:" \
		"    k0rdent.mirantis.com/managed: \"true\"" \
		"spec:" \
		"  type: oci" \
		"  url: $(KCM_REPO_URL)" | $(KUBECTL) -n $(NAMESPACE) create -f -
	@curl -s "https://api.github.com/repos/k0rdent/kcm/contents/templates/provider/kcm-templates/files/templates?ref=$(KCM_STABLE_VERSION)" | \
	jq -r '.[].download_url' | while read url; do \
		curl -s "$$url" | \
		$(YQ) '.spec.helm.chartSpec.sourceRef.name = "$(KCM_REPO_NAME)"' | \
		$(KUBECTL) -n $(NAMESPACE) create -f - || true; \
	done

.PHONY: dev-release
dev-release:
	@$(YQ) e ".spec.version = \"${VERSION}\"" $(PROVIDER_TEMPLATES_DIR)/kcm-templates/files/release.yaml | $(KUBECTL) -n $(NAMESPACE) apply -f -

.PHONY: dev-adopted-creds
dev-adopted-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/adopted-credentials.yaml | $(KUBECTL) apply -f -

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

.PHONY: dev-docker-creds
dev-docker-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/docker-credentials.yaml | $(KUBECTL) apply -f -

.PHONY: dev-remote-creds
dev-remote-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/remote-credentials.yaml | $(KUBECTL) apply -f -

.PHONY: dev-gcp-creds
dev-gcp-creds: envsubst
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/dev/gcp-credentials.yaml | $(KUBECTL) apply -f -

.PHONY: dev-apply
dev-apply: kind-deploy registry-deploy dev-push dev-deploy dev-templates dev-release ## Apply the development environment by deploying the kind cluster, local registry and the KCM helm chart.

PUBLIC_REPO ?= false

.PHONY: test-apply
test-apply: kind-deploy
	@if [ "$(PUBLIC_REPO)" != "true" ]; then \
	  $(MAKE) registry-deploy dev-push; \
	fi; \
	$(MAKE) dev-deploy dev-templates dev-release

.PHONY: dev-destroy
dev-destroy: kind-undeploy registry-undeploy ## Destroy the development environment by deleting the kind cluster and local registry.

.PHONY: support-bundle
support-bundle: SUPPORT_BUNDLE_OUTPUT=$(CURDIR)/support-bundle-$(shell date +"%Y-%m-%dT%H_%M_%S")
support-bundle: envsubst support-bundle-cli
	@NAMESPACE=$(NAMESPACE) $(ENVSUBST) -no-unset -i config/support-bundle.yaml | $(SUPPORT_BUNDLE_CLI) -o $(SUPPORT_BUNDLE_OUTPUT) --debug -

.PHONY: dev-mcluster-apply
dev-mcluster-apply: envsubst ## Create dev managed cluster using 'config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml'
	@NAMESPACE=$(NAMESPACE) CLUSTER_NAME_SUFFIX=$(CLUSTER_NAME_SUFFIX) $(ENVSUBST) -no-unset -i config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml | $(KUBECTL) apply -f -

.PHONY: dev-mcluster-delete
dev-mcluster-delete: envsubst ## Delete dev managed cluster using 'config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml'
	@NAMESPACE=$(NAMESPACE) CLUSTER_NAME_SUFFIX=$(CLUSTER_NAME_SUFFIX) $(ENVSUBST) -no-unset -i config/dev/$(DEV_PROVIDER)-clusterdeployment.yaml | $(KUBECTL) delete -f -

.PHONY: dev-creds-apply
dev-creds-apply: dev-$(DEV_PROVIDER)-creds ## Create credentials resources for $DEV_PROVIDER

.PHONY: dev-aws-nuke
dev-aws-nuke: envsubst awscli yq cloud-nuke ## Warning: Destructive! Nuke all AWS resources deployed by 'DEV_PROVIDER=aws dev-mcluster-apply'
	@CLUSTER_NAME=$(CLUSTER_NAME) YQ=$(YQ) AWSCLI=$(AWSCLI) $(SHELL) $(CURDIR)/scripts/aws-nuke-ccm.sh elb
	@CLUSTER_NAME=$(CLUSTER_NAME) $(ENVSUBST) < config/dev/aws-cloud-nuke.yaml.tpl > config/dev/aws-cloud-nuke.yaml
	DISABLE_TELEMETRY=true $(CLOUDNUKE) aws --region $(AWS_REGION) --force --config config/dev/aws-cloud-nuke.yaml --resource-type vpc,eip,nat-gateway,ec2,ec2-subnet,elb,elbv2,ebs,internet-gateway,network-interface,security-group,ekscluster
	@rm config/dev/aws-cloud-nuke.yaml
	@CLUSTER_NAME=$(CLUSTER_NAME) YQ=$(YQ) AWSCLI=$(AWSCLI) $(SHELL) $(CURDIR)/scripts/aws-nuke-ccm.sh ebs

.PHONY: dev-azure-nuke
dev-azure-nuke: envsubst azure-nuke ## Warning: Destructive! Nuke all Azure resources deployed by 'DEV_PROVIDER=azure dev-mcluster-apply'
	@CLUSTER_NAME=$(CLUSTER_NAME) $(ENVSUBST) -no-unset < config/dev/azure-cloud-nuke.yaml.tpl > config/dev/azure-cloud-nuke.yaml
	$(AZURENUKE) run --config config/dev/azure-cloud-nuke.yaml --force --no-dry-run
	@rm config/dev/azure-cloud-nuke.yaml

.PHONY: kubevirt
kubevirt: KUBEVIRT_VERSION = $(shell curl -s https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt)
kubevirt: CDI_VERSION = $(shell basename $$(curl -s -w '%{redirect_url}' -o /dev/null https://github.com/kubevirt/containerized-data-importer/releases/latest))
kubevirt:
	@echo Installing KubeVirt $(KUBEVIRT_VERSION)
	@echo Installing Containerized Data Importer $(CDI_VERSION)
	kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/$(KUBEVIRT_VERSION)/kubevirt-operator.yaml
	kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/$(KUBEVIRT_VERSION)/kubevirt-cr.yaml
	kubectl apply -f https://github.com/kubevirt/containerized-data-importer/releases/download/$(CDI_VERSION)/cdi-operator.yaml
	kubectl apply -f https://github.com/kubevirt/containerized-data-importer/releases/download/$(CDI_VERSION)/cdi-cr.yaml

	@echo "Waiting for KubeVirt to be deployed..."
	@timeout=900; interval=10; \
	while [ $$timeout -gt 0 ]; do \
		status=$$(kubectl get kubevirt.kubevirt.io/kubevirt -n kubevirt -o=jsonpath="{.status.phase}" 2>/dev/null || echo ""); \
		if [ "$$status" = "Deployed" ]; then \
			echo "KubeVirt is deployed"; \
			exit 0; \
		fi; \
		echo "KubeVirt is deploying..."; \
		sleep $$interval; \
		timeout=$$((timeout-interval)); \
	done; \
	echo "Timeout reached. KubeVirt is not deployed."; \
	exit 1

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

SVELTOS_VERSION ?= v$(shell grep 'appVersion:' $(PROVIDER_TEMPLATES_DIR)/projectsveltos/Chart.yaml | cut -d ' ' -f 2)
SVELTOS_NAME ?= sveltos
SVELTOS_CRD ?= $(EXTERNAL_CRD_DIR)/$(SVELTOS_NAME)-$(SVELTOS_VERSION).yaml

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

capi-operator-crds: CAPI_OPERATOR_VERSION=v$(shell $(YQ) -r '.dependencies.[] | select(.name == "cluster-api-operator") | .version' $(PROVIDER_TEMPLATES_DIR)/kcm/Chart.yaml)
capi-operator-crds: CAPI_OPERATOR_CRD_PREFIX="operator.cluster.x-k8s.io_"
capi-operator-crds: | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(CAPI_OPERATOR_CRD_PREFIX)*
	@$(foreach name, \
		addonproviders bootstrapproviders controlplaneproviders coreproviders infrastructureproviders ipamproviders runtimeextensionproviders, \
		curl -s --fail https://raw.githubusercontent.com/kubernetes-sigs/cluster-api-operator/$(CAPI_OPERATOR_VERSION)/config/crd/bases/$(CAPI_OPERATOR_CRD_PREFIX)${name}.yaml \
		> $(EXTERNAL_CRD_DIR)/$(CAPI_OPERATOR_CRD_PREFIX)${name}-$(CAPI_OPERATOR_VERSION).yaml;)

cluster-api-crds: CLUSTER_API_VERSION=$(shell go mod edit -json | jq -r '.Require[] | select(.Path == "sigs.k8s.io/cluster-api") | .Version')
cluster-api-crds: CLUSTER_API_CRD_PREFIX="cluster.x-k8s.io_"
cluster-api-crds: | $(EXTERNAL_CRD_DIR)
	rm -f $(EXTERNAL_CRD_DIR)/$(CLUSTER_API_CRD_PREFIX)*
	@$(foreach name, \
		clusters machinedeployments, \
		curl -s --fail https://raw.githubusercontent.com/kubernetes-sigs/cluster-api/$(CLUSTER_API_VERSION)/config/crd/bases/$(CLUSTER_API_CRD_PREFIX)${name}.yaml \
		> $(EXTERNAL_CRD_DIR)/$(CLUSTER_API_CRD_PREFIX)${name}-$(CLUSTER_API_VERSION).yaml;)

.PHONY: external-crd
external-crd: $(FLUX_HELM_CRD) $(FLUX_SOURCE_CHART_CRD) $(FLUX_SOURCE_REPO_CRD) $(FLUX_SOURCE_GITREPO_CRD) $(FLUX_SOURCE_BUCKET_CRD) $(FLUX_SOURCE_OCIREPO_CRD) $(VELERO_BACKUP_CRD) $(SVELTOS_CRD) capi-operator-crds cluster-api-crds

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
ENVTEST_VERSION ?= release-0.20
GOLANGCI_LINT_VERSION ?= v1.64.8
GOLANGCI_LINT_TIMEOUT ?= 1m
HELM_VERSION ?= v3.17.2
KIND_VERSION ?= v0.27.0
YQ_VERSION ?= v4.45.1
CLOUDNUKE_VERSION = v0.38.2
AZURENUKE_VERSION = v1.2.0
CLUSTERAWSADM_VERSION ?= v2.7.1
CLUSTERCTL_VERSION ?= v1.9.4
ADDLICENSE_VERSION ?= v1.1.1
ENVSUBST_VERSION ?= v1.4.2
AWSCLI_VERSION ?= 2.17.42
SUPPORT_BUNDLE_CLI_VERSION ?= v0.117.0

.PHONY: cli-install
cli-install: controller-gen envtest golangci-lint helm kind yq cloud-nuke azure-nuke clusterawsadm clusterctl addlicense envsubst awscli ## Install the necessary CLI tools for deployment, development and testing.

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
envsubst: $(ENVSUBST)
$(ENVSUBST): | $(LOCALBIN)
	$(call go-install-tool,$(ENVSUBST),github.com/a8m/envsubst/cmd/envsubst,${ENVSUBST_VERSION})

.PHONY: awscli
awscli: $(AWSCLI)
$(AWSCLI): | $(LOCALBIN)
	@if [ $(HOSTOS) == "linux" ]; then \
		for i in unzip curl; do \
			command -v $$i >/dev/null 2>&1 || { \
				echo "$$i is not installed. Please install $$i manually."; \
				exit 1; \
			}; \
		done; \
		curl --fail "https://awscli.amazonaws.com/awscli-exe-linux-$(shell uname -m)-$(AWSCLI_VERSION).zip" -o "/tmp/awscliv2.zip" && \
		unzip -oqq /tmp/awscliv2.zip -d /tmp && \
		/tmp/aws/install -i $(LOCALBIN)/aws-cli -b $(LOCALBIN) --update && \
		ln -s $(LOCALBIN)/aws-cli/v2/current/bin/aws $(AWSCLI) || true; \
	fi; \
	if [ $(HOSTOS) == "darwin" ]; then \
		curl --fail "https://awscli.amazonaws.com/AWSCLIV2.pkg" -o $(CURDIR)/AWSCLIV2.pkg && \
		LOCALBIN="$(LOCALBIN)" $(ENVSUBST) -i $(CURDIR)/scripts/awscli-darwin-install.xml.tpl > $(CURDIR)/choices.xml && \
		installer -pkg $(CURDIR)/AWSCLIV2.pkg -target CurrentUserHomeDirectory -applyChoiceChangesXML $(CURDIR)/choices.xml && \
		ln -s $(LOCALBIN)/aws-cli/aws $(AWSCLI) || true && \
		rm $(CURDIR)/AWSCLIV2.pkg && rm $(CURDIR)/choices.xml; \
	fi; \
	if [ $(HOSTOS) == "windows" ]; then \
		echo "Installing to $(LOCALBIN) on Windows is not yet implemented" && \
		exit 1; \
	fi; \

.PHONY: support-bundle-cli
support-bundle-cli: $(SUPPORT_BUNDLE_CLI) ## Download support-bundle locally if necessary.
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
