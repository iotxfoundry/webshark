module github.com/iotxfoundry/webshark

go 1.27.0

// 前端工程（web/ui）的 node_modules 里随包携带的第三方 Go 文件不参与本模块构建
ignore web/ui/node_modules

require (
	github.com/fsnotify/fsnotify v1.10.1
	github.com/gorilla/websocket v1.5.3
	github.com/stretchr/testify v1.12.1
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.13.0 // indirect
)
