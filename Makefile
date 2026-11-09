APP      := remote-host-cli
PREFIX   ?= /usr/local
BINDIR   ?= $(PREFIX)/bin
BIN      := bin/$(APP)
GO       ?= go
LDFLAGS  ?= -s -w

DSH_HOME      ?= $(HOME)/.dsh
SKILL_NAME    := remote-host-cli
SKILL_SRC     := skills/$(SKILL_NAME)/SKILL.md
SKILL_DEST    := $(DSH_HOME)/skills/$(SKILL_NAME)/SKILL.md

.PHONY: all build install install-user uninstall uninstall-user install-skill uninstall-skill test vet fmt clean

all: build

build:
	@mkdir -p bin
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) .

# System-wide install (usually requires sudo): sudo make install
install: build
	@if [ -z "$(DESTDIR)" ] && [ ! -w "$(BINDIR)" ]; then \
		echo "错误: 没有写入 $(BINDIR) 的权限"; \
		echo "系统级安装: sudo make install"; \
		echo "用户级安装: make install-user"; \
		exit 1; \
	fi
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 0755 "$(BIN)" "$(DESTDIR)$(BINDIR)/$(APP)"
	@echo "已安装: $(DESTDIR)$(BINDIR)/$(APP)"
	@echo "首次使用请运行: $(APP) init"
	@echo "默认配置: ~/.remote-host-cli/config.json"

# Per-user install: ~/.local/bin/remote-host-cli
install-user:
	$(MAKE) install PREFIX="$(HOME)/.local" BINDIR="$(HOME)/.local/bin"

uninstall:
	rm -f "$(DESTDIR)$(BINDIR)/$(APP)"
	@echo "已移除: $(DESTDIR)$(BINDIR)/$(APP)（配置文件未删除）"

uninstall-user:
	$(MAKE) uninstall PREFIX="$(HOME)/.local" BINDIR="$(HOME)/.local/bin"

# Install the agent skill into DSH: ~/.dsh/skills/remote-host-cli/SKILL.md
install-skill:
	install -d "$(DSH_HOME)/skills/$(SKILL_NAME)"
	install -m 0644 "$(SKILL_SRC)" "$(SKILL_DEST)"
	@echo "已安装 DSH skill: $(SKILL_DEST)"

uninstall-skill:
	rm -f "$(SKILL_DEST)"
	-rmdir "$(DSH_HOME)/skills/$(SKILL_NAME)" 2>/dev/null || true
	@echo "已移除 DSH skill: $(SKILL_DEST)"

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w main.go main_test.go shell.go resize_signal_*.go internal/config/*.go internal/remote/*.go

clean:
	rm -rf bin
