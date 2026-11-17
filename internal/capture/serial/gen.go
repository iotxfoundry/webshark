package serial

// go:generate 生成三架构（amd64/arm64/arm，均小端）的 bpf_*_bpfel.o 与对应 Go 装载
// 文件（按 GOARCH build tag 三选一），产物入库，CI 不现场编译。重生成需本机
// clang ≥ 11 + llvm-strip 在 PATH（如 /usr/lib/llvm-18/bin）与
// /sys/kernel/btf/vmlinux（bpf/vmlinux.h 由 `bpftool btf dump file
// /sys/kernel/btf/vmlinux format c > bpf/vmlinux.h` 再生，随内核版本更新）：
//
//	go generate ./internal/capture/serial/
//
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64,arm64,arm bpf bpf/serial_bpf.c -- -I./bpf -O2 -g -Wall
