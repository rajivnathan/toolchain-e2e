# Deferred so QUAY_NAMESPACE from the environment (or make/test.mk) is expanded at use time.
PERFAPP_IMAGE_TAG ?= $(GIT_COMMIT_ID)
PERFAPP_IMAGE = quay.io/$(QUAY_NAMESPACE)/perfapp:$(PERFAPP_IMAGE_TAG)
IMAGE_PLATFORM ?= linux/amd64

define require-quay-namespace
	@test -n "$(QUAY_NAMESPACE)" || (echo "QUAY_NAMESPACE is required (export QUAY_NAMESPACE=<quay-username>)" && exit 1)
endef

.PHONY: perfapp-build
## Build the perfapp binary
perfapp-build:
	@mkdir -p $(OUT_DIR)/bin
	$(Q)CGO_ENABLED=0 go build -o $(OUT_DIR)/bin/perfapp ./perfapp

.PHONY: perfapp-image
## Build the perfapp image tagged with the git SHA (quay.io/$(QUAY_NAMESPACE)/perfapp:commit)
perfapp-image:
	$(call require-quay-namespace)
	@echo "building $(PERFAPP_IMAGE) with podman..."
	podman build --platform $(IMAGE_PLATFORM) -t $(PERFAPP_IMAGE) -f build/perfapp/Dockerfile .

.PHONY: perfapp-deploy
## Apply deploy/onboarding-perfapp with image quay.io/$(QUAY_NAMESPACE)/perfapp:$(PERFAPP_IMAGE_TAG)
perfapp-deploy:
	$(call require-quay-namespace)
	oc kustomize deploy/onboarding-perfapp | sed 's|image: quay.io/QUAY_NAMESPACE/perfapp:GIT_SHA|image: $(PERFAPP_IMAGE)|' | oc apply -f -
