# GitHub repository
REPO := sspaeti/neomd
INSTALL_PATH := ~/.local/bin/neomd

.DEFAULT_GOAL := run-headless

## Reinstall from latest GitHub release and restart daemon
reinstall: stop
	@echo "Fetching latest release from GitHub..."
	@latest=$$(curl -s https://api.github.com/repos/$(REPO)/releases/latest | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/'); \
	echo "Latest version: $$latest"; \
	echo "Downloading neomd-freebsd..."; \
	curl -L -o /tmp/neomd-freebsd "https://github.com/$(REPO)/releases/download/$$latest/neomd-freebsd"; \
	echo "Installing to $(INSTALL_PATH)..."; \
	mkdir -p ~/.local/bin; \
	mv /tmp/neomd-freebsd $(INSTALL_PATH); \
	chmod +x $(INSTALL_PATH); \
	echo "Done! Installed $$latest"
	@$(MAKE) run-headless
	@sleep 1
	@$(MAKE) status

run-headless:
	nohup neomd --headless >> ~/.local/share/neomd/daemon.log 2>&1 &

log:
	tail -f ~/.local/share/neomd/daemon.log

restart: stop run-headless

stop:
	pkill neomd || true
	sleep 1

status:
	ps aux | grep neomd | grep -v grep || echo "neomd is not running"

version:
	neomd --version
