PLUGIN_BINARY := bin/dokku-vault-agent
GO ?= go

COMMANDS := \
	ca:clear \
	ca:set \
	configure \
	disable \
	enable \
	render \
	report \
	role-id:set \
	stage \
	template:add \
	template:clear-custom \
	template:list \
	template:remove \
	template:set-custom

TRIGGERS := \
	post-app-clone-setup \
	post-app-rename-setup \
	post-deploy \
	pre-delete \
	pre-release-builder

.PHONY: build clean link-files links test

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(PLUGIN_BINARY) ./cmd/dokku-vault-agent

link-files:
	mkdir -p subcommands
	for command in $(COMMANDS); do ln -sfn ../$(PLUGIN_BINARY) "subcommands/$$command"; done
	for trigger in $(TRIGGERS); do ln -sfn $(PLUGIN_BINARY) "$$trigger"; done

links: build link-files

test:
	$(GO) test ./...
	./tests/install_test.sh

clean:
	rm -rf bin subcommands $(TRIGGERS) coverage.out
