//go:build ignore

// serial_bpf.c 串口采集 eBPF：kprobe 挂 tty 层，TX=n_tty_write / RX=n_tty_receive_buf_common，
// 字节切块经 ringbuf 上送用户态。结构体字段 CO-RE 读目标内核 BTF
//（vmlinux.h 由 bpftool btf dump 生成）。产物入库：
//
//	go generate ./internal/capture/serial/
//
// 需 clang ≥ 11 与 /sys/kernel/btf/vmlinux。

#include "vmlinux.h"
// 三架构产物均在本机（x86 内核 BTF 生成的 vmlinux.h）交叉编译：该头里寄存器布局
// 相关结构按宿主架构展开，与目标架构取参所需形态不符，按各目标补齐后交
// bpf_tracing.h 的 BPF_KPROBE 取参。结构体字段访问一律 CO-RE 按目标内核 BTF
// 解析，vmlinux.h 的架构差异不影响字段正确性。
#if defined(__TARGET_ARCH_arm64)
// vmlinux.h 只有 user_pt_regs 前置声明，PT_REGS_ARM64 读参需要完整布局，
// 此处补全（aarch64 标准布局 31 通用寄存器+sp+pc+pstate）。
struct user_pt_regs {
	__u64 regs[31];
	__u64 sp;
	__u64 pc;
	__u64 pstate;
};
#endif
#include "bpf_helpers.h"
#include "bpf_tracing.h"
#if defined(__TARGET_ARCH_arm)
// vmlinux.h 的 struct pt_regs 是 x86 布局（无 uregs 字段），bpf_tracing.h 的
// arm 分支按 ->uregs[i] 读参编译不过。arm32 内核 pt_regs 首成员即
// uregs[18]（r0-r12,sp,lr,pc…），按字偏移 4*i 等价取参，语义同 libbpf arm 分支。
// 重定义须在 include 之后：bpf_tracing.h 的 arm 分支定义会覆盖先行定义，
// 而 BPF_KPROBE 的展开点在其后的程序函数处，此处重定义即最终生效。
#undef PT_REGS_PARM1
#undef PT_REGS_PARM2
#undef PT_REGS_PARM3
#undef PT_REGS_PARM4
#undef PT_REGS_PARM5
#define PT_REGS_PARM1(x) (((const volatile __u32 *)(x))[0])
#define PT_REGS_PARM2(x) (((const volatile __u32 *)(x))[1])
#define PT_REGS_PARM3(x) (((const volatile __u32 *)(x))[2])
#define PT_REGS_PARM4(x) (((const volatile __u32 *)(x))[3])
#define PT_REGS_PARM5(x) (((const volatile __u32 *)(x))[4])
#endif

char LICENSE[] SEC("license") = "GPL";

// 事件头：用户态按 tty_name 分流 + 还原方向/时刻；data 变长载荷紧随其后。
// 切块上限 4KB：tty 层单次 write/receive 不超过其 buf 容量，4KB 覆盖常规 RTU 突发。
#define MAX_CHUNK 4096
// tty_name 长度与 struct tty_struct.name 对齐。
#define TTY_NAME_LEN 64

struct event_t {
	u64 ts_ns;                // bpf_ktime_get_ns() 单调时刻
	u8 dir;                   // 0=TX(n_tty_write) 1=RX(receive_buf)
	u8 _pad[7];
	u32 len;                  // 本块有效字节数
	char tty_name[TTY_NAME_LEN];
	u8 data[MAX_CHUNK];       // 字节载荷（变长，用户态按 len 读）
};
// 注意：本地结构体不加 preserve_access_index——内核侧 CO-RE 对本地类型也要按名去内核 BTF
// 找候选，找不到即拒绝装载("invalid func unknown#...")；本地内存访问本就不需要 CO-RE。
// -type event_t 的 Go 绑定依赖 -g 保留的 BTF，与该属性无关。

struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 1 << 24); // 16MB：工业串口速率低，余量防突发
} events SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__uint(max_entries, 1);
	__type(key, u32);
	__type(value, u32);
} dropped SEC(".maps"); // ringbuf reserve 失败计数，用户态随状态上报

// put_event 通用上送：reserve 失败计数丢弃，拷贝 min(count, MAX_CHUNK)。
static __always_inline int put_event(const struct tty_struct *tty, u8 dir,
				     const u8 *buf, size_t count)
{
	struct event_t *e;
	size_t n = count > MAX_CHUNK ? MAX_CHUNK : count;
	u32 key = 0;

	e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
	if (!e) {
		u32 *d = bpf_map_lookup_elem(&dropped, &key);
		if (d)
			(*d)++;
		return 0;
	}
	e->ts_ns = bpf_ktime_get_ns();
	e->dir = dir;
	e->len = (u32)n;
	bpf_probe_read_kernel_str(e->tty_name, TTY_NAME_LEN, tty->name);
	if (n)
		bpf_probe_read_kernel(e->data, n, buf);
	bpf_ringbuf_submit(e, 0);
	return 0;
}

// 两函数均为 static：现代内核 vmlinux BTF 不收录 static 函数，fentry 无从解析 BTF id，
// 故走 kprobe（kallsyms 基，BPF_KPROBE 按 PT_REGS 读参，结构体字段经 CO-RE 读 BTF 结构类型）。
// n_tty_write(struct tty_struct *tty, struct file *file, const u8 *buf, size_t nr)
SEC("kprobe/n_tty_write")
int BPF_KPROBE(tty_tx, struct tty_struct *tty, struct file *file,
	       const u8 *buf, size_t nr)
{
	return put_event(tty, 0, buf, nr);
}

// n_tty_receive_buf_common(struct tty_struct *tty, const u8 *cp, const u8 *fp,
//                          size_t count, bool flow)
SEC("kprobe/n_tty_receive_buf_common")
int BPF_KPROBE(tty_rx, struct tty_struct *tty, const u8 *cp, const u8 *fp,
	       size_t count, bool flow)
{
	return put_event(tty, 1, cp, count);
}
