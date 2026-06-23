BINARY  := /usr/local/bin/echomux
SERVICE := echomux

.PHONY: build ui install deploy

-include Makefile.local

ui:
	cd service/ui && npm run build

build: ui
	cd service && go build -o echomux ./cmd/echomux/

install: build
	sudo systemctl stop $(SERVICE)
	sudo cp service/echomux $(BINARY)
	sudo systemctl start $(SERVICE)

deploy: install
	@echo "Deployed. Service status:"
	@systemctl is-active $(SERVICE)
