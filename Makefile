GO ?= go
BIN := bin

# 显式列包：web/node_modules 里会有第三方 Go 包，go ./... 会把它扫进构建范围
PKGS := . ./cmd/... ./internal/... ./web

# 打包时归一化 mtime：内容不变则 zip 字节不变，避免无意义二进制差异
ZIP_EPOCH := 202001010000.00

.PHONY: all build web test vet fmt clean

# 全量构建：前端产物 + 二进制
all: web build

# 仅 Go 二进制（web/dist.zip 已入库，前端未改时无需 node）
build:
	$(GO) build -trimpath -o $(BIN)/webshark ./cmd/webshark

# 前端构建并打包为 web/dist.zip（入库产物，供 //go:embed 使用）
web:
	cd web && yarn install --immutable && yarn build
	find web/dist -exec touch -t $(ZIP_EPOCH) {} +
	cd web/dist && rm -f ../dist.zip && zip -qr -X ../dist.zip .

test:
	$(GO) test $(PKGS)

vet:
	$(GO) vet $(PKGS)

fmt:
	gofmt -l -w .

# 只清构建产物；web/dist.zip 是入库产物，不在此列（用 make web 重新生成）
clean:
	rm -rf $(BIN) web/dist
