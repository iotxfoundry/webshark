module github.com/iotxfoundry/webshark

go 1.27.0

// 前端工程整体不参与本模块构建（其 node_modules 里随包携带第三方 Go 文件）
ignore web/ui

require (
	github.com/fsnotify/fsnotify v1.10.1
	github.com/gorilla/websocket v1.5.3
	github.com/stretchr/testify v1.12.1
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.13.0 // indirect
)
