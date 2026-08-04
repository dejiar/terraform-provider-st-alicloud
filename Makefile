# The name of Terraform custom provider.
CUSTOM_PROVIDER_NAME ?= terraform-provider-st-alicloud
# The url of Terraform provider.
CUSTOM_PROVIDER_URL ?= example.local/myklst/st-alicloud

GO_INSTALL_PATH := $(shell go env GOBIN)

ifeq ($(GO_INSTALL_PATH),)
GO_INSTALL_PATH := $(shell go env GOPATH)/bin
endif

install-local-custom-provider:
	export PROVIDER_LOCAL_PATH='example.local/myklst/st-alicloud'
	go install .
	HOME_DIR="$(HOME)"; \
	mkdir -p $$HOME_DIR/.terraform.d/plugins/example.local/myklst/st-alicloud/0.1.0/linux_amd64/; \
	cp $(GO_INSTALL_PATH)/terraform-provider-st-alicloud \
	   $$HOME_DIR/.terraform.d/plugins/example.local/myklst/st-alicloud/0.1.0/linux_amd64/terraform-provider-st-alicloud
