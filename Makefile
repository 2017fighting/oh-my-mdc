# make 是构建门的唯一真源：CI 只调这里的 target，不直接写命令（规格 §10）。
SHELL := /bin/bash
.DEFAULT_GOAL := help

# 版本由 git tag 注入（发布流水线），本地一律 dev。
VERSION ?= dev
LDFLAGS := -X main.version=$(VERSION)

GO      ?= go
PNPM    ?= pnpm
COMPOSE ?= docker compose

BIN_DIR   := bin
BINARY    := $(BIN_DIR)/oh-my-mdc
WEB_DIR   := web
WEB_DIST  := internal/web/dist

# 开发用的 Postgres：端口与凭据跟 compose.yaml、config.DefaultDatabaseURL 对齐。
TEST_DATABASE_URL ?= postgres://mdc:mdc@localhost:5432/mdc?sslmode=disable
# 测试会在这个库上自建一次性库再删掉。留空＝跳过数据库集成测试（CI 用服务容器注入它）。
# 用 ?= 是为了让环境变量/命令行覆盖生效。
MDC_TEST_DATABASE_URL ?=

.PHONY: help
help: ## 列出所有 target
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- 开发

.PHONY: dev
dev: db-up web-build ## 起完整本地栈（compose Postgres + 服务），零配置
	@echo "→ 打开 http://localhost:8080/"
	$(GO) run -ldflags "$(LDFLAGS)" ./cmd/oh-my-mdc

.PHONY: db-up
db-up: ## 起 Postgres 并等到 healthy
	$(COMPOSE) up -d --wait postgres

.PHONY: db-down
db-down: ## 停 Postgres（保留数据卷）
	$(COMPOSE) down

.PHONY: db-reset
db-reset: ## 停 Postgres 并删数据卷（下次 db-up 是空库）
	$(COMPOSE) down --volumes

# ---------------------------------------------------------------- 构建

.PHONY: build
build: web-build ## 编译单二进制（前端先构建再 embed）
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/oh-my-mdc

.PHONY: web-install
web-install: ## 安装前端依赖
	cd $(WEB_DIR) && $(PNPM) install --frozen-lockfile

.PHONY: web-lint
web-lint: web-install ## 前端 lint
	cd $(WEB_DIR) && $(PNPM) lint

.PHONY: web-typecheck
web-typecheck: web-install ## 前端类型检查
	cd $(WEB_DIR) && $(PNPM) typecheck

.PHONY: web-build
web-build: web-install ## vite build → internal/web/dist（embed 的输入）
	cd $(WEB_DIR) && $(PNPM) build

# ---------------------------------------------------------------- 检查

.PHONY: fmt
fmt: ## gofmt 改写
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## 校验 gofmt（CI 用）
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "gofmt 未通过:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: test
test: ## 跑测试（-race）；没给 MDC_TEST_DATABASE_URL 就跳过数据库集成测试
	MDC_TEST_DATABASE_URL='$(MDC_TEST_DATABASE_URL)' $(GO) test -race ./...

.PHONY: test-local
test-local: db-up ## 本地全量：先起 compose Postgres 再跑测试（含集成测试）
	MDC_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' $(GO) test -race ./...

.PHONY: check
check: fmt-check vet test ## CI 的 Go 门（数据库由 CI 的服务容器提供）

.PHONY: check-local
check-local: fmt-check vet web-lint web-typecheck test-local ## 本地全门

.PHONY: web-reset
web-reset: ## 把 internal/web/dist 恢复成仓库里的占位壳页
	git checkout -- $(WEB_DIST)

.PHONY: clean
clean: ## 清掉构建产物（保留仓库里的占位 dist）
	rm -rf $(BIN_DIR) $(WEB_DIST)/assets
