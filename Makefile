GO ?= go
BIN := bin

# 显式列包：web/node_modules 里会有第三方 Go 包，go ./... 会把它扫进构建范围
PKGS := ./cmd/... ./internal/... ./pkg/... ./web

.PHONY: all build web test vet fmt clean

# 全量构建：前端产物 + 二进制
all: web build

# 仅 Go 二进制（前端未构建时服务端回退提示页）
build:
	$(GO) build -trimpath -o $(BIN)/webshark ./cmd/webshark

# 前端构建（输出 web/dist）
web:
	cd web && yarn install --immutable && yarn build

test:
	$(GO) test $(PKGS)

vet:
	$(GO) vet $(PKGS)

fmt:
	gofmt -l -w .

clean:
	rm -rf $(BIN) web/dist
