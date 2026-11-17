package web

import (
	"io"
	"io/fs"
	"sync"
	"testing"
)

// TestFSConcurrentReads 锁定并发安全：浏览器并行拉取静态资源（CSS+JS+多字体）
// 即多个 goroutine 同时 Open/Read 同一个 FS。共享 zip.Reader 会偶发 seek 失败
// （ServeContent 报 500 "seeker can't seek"），逐 Open 重建 Reader 则必须全绿。
func TestFSConcurrentReads(t *testing.T) {
	f := FS()
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				// 索引页 + 字体（Deflate 条目，覆盖读路径）
				name := "assets/fira-code-latin-400-normal-DGosTW8U.woff2"
				if j%2 == 0 {
					name = "index.html"
				}
				rc, err := f.Open(name)
				if err != nil {
					errs <- err
					continue
				}
				if _, err := io.Copy(io.Discard, rc); err != nil {
					errs <- err
				}
				_ = rc.Close()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发读失败: %v", err)
	}
}

// TestFSMissing 站点包损坏时 Open 回 ErrNotExist（调用方回退提示页）。
func TestFSMissing(t *testing.T) {
	if _, err := zipFS(nil).Open("index.html"); err == nil {
		t.Fatal("空包应报错")
	}
	if _, err := fs.Stat(FS(), "index.html"); err != nil {
		t.Fatalf("已构建包应含 index.html: %v", err)
	}
}
