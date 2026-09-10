PLUGIN_BINARY := bin/dokku-vault-agent
GO ?= go

COMMANDS := \
	ca:clear \
	ca:set \
	configure \
	disable \
	enable \
	help \
	render \
	report \
	role-id:set \
	stage \
	template:add \
	template:clear-custom \
	template:list \
	template:remove \
	template:set-custom

BINARY_TRIGGERS := \
	post-app-clone-setup \
	post-app-rename-setup \
	post-deploy \
	pre-delete

WRAPPER_TRIGGERS := pre-release-builder

.PHONY: build clean link-files links test

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(PLUGIN_BINARY) ./cmd/dokku-vault-agent

link-files:
	mkdir -p subcommands
	ln -sfn ../$(PLUGIN_BINARY) subcommands/default
	for command in $(COMMANDS); do ln -sfn ../$(PLUGIN_BINARY) "subcommands/$$command"; done
	for trigger in $(BINARY_TRIGGERS); do ln -sfn $(PLUGIN_BINARY) "$$trigger"; done
	ln -sfn triggers/pre-release-builder pre-release-builder

links: build link-files

test:
	$(GO) test ./...
	./tests/install_test.sh

clean:
	rm -rf bin subcommands $(BINARY_TRIGGERS) $(WRAPPER_TRIGGERS) coverage.out
