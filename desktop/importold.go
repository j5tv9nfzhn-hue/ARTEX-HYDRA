package main

// 旧版 WEB 端（artex-rea）数据导入：把旧安装目录里的用户数据拷进当前桌面端
// 安装目录，实现「无感延续」。
//
// 设计要点：
//   - 只拷贝、不移动——旧目录全程只读，用户随时可以切回旧版；
//   - 前置校验硬拒绝：旧目录必须含 artex.db；8787/8788 端口必须空闲
//     （旧版 2.5.0.5 没有单实例锁，只有端口占用退出，所以用端口探测判断
//     「旧应用是否已完全退出」——运行中拷 SQLite 会拷出半截事务）；
//   - 新安装目录已存在 artex.db 时拒绝（防重复导入覆盖新数据）；
//   - config.json 只保留 listen/language：旧配置里的 database.path/skill_dir
//     指向旧目录的绝对路径，带过来会让新应用继续读写旧目录；
//   - skills 整树旧覆盖新：用户在技能页改过的文件全保留，被覆盖的内置技能
//     可在 UI「恢复出厂」升到新版快照；
//   - 根目录的 i<意图ID> 产物目录（旧版 worker 的 CWD 回退产物）best-effort
//     拷进新 data/，保证 DB 里引用的 recon 等输出文件仍可打开。

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Autumn-27/artex/config"
)

// legacyPorts 是旧版 WEB 端占用的端口（HTTP/API 与流量记录代理）。
// 两者任一仍在监听，都说明旧实例没有完全退出。
var legacyPorts = []string{"8787", "8788"}

// legacyRunDirRe 匹配旧版 worker 落在安装根目录的意图产物目录（i2638 这类）。
var legacyRunDirRe = regexp.MustCompile(`^i\d+$`)

// importFailed 汇总导入失败的项；非致命项失败不中断整体导入。
var importFailed []string

// runImportOld 执行导入并返回进程退出码。
func runImportOld(oldDir string) int {
	oldDir = filepath.Clean(strings.TrimSpace(oldDir))
	if oldDir == "" || oldDir == "." {
		fmt.Fprintln(os.Stderr, "用法：artex-hydra-desktop -import-old <旧版 WEB 端目录>")
		return 1
	}

	// ① 旧目录有效性：artex.db 是数据存在的标志，artex-rea.exe/versions 是
	//    「这确实是旧版安装目录」的加固判断。
	if _, err := os.Stat(filepath.Join(oldDir, "artex.db")); err != nil {
		fmt.Fprintf(os.Stderr, "[import] %s 不是有效的旧版安装目录：找不到 artex.db\n", oldDir)
		return 1
	}
	if !legacyInstallMarker(oldDir) {
		fmt.Fprintf(os.Stderr, "[import] 警告：%s 下没有 artex-rea.exe/versions，请确认这是旧版安装目录\n", oldDir)
	}

	// ② 旧应用必须已完全退出：任一 legacy 端口仍在监听就拒绝。
	for _, port := range legacyPorts {
		if portInUse(port) {
			fmt.Fprintf(os.Stderr, "[import] 端口 %s 仍被监听：旧版程序可能正在运行。请完全退出旧版（含流量记录代理）后重试。\n", port)
			return 1
		}
	}

	// ③ 新安装目录不能已有 artex.db：重复导入会用旧数据覆盖新产生的数据。
	newRoot := config.BaseDir()
	if newRoot == oldDir {
		fmt.Fprintln(os.Stderr, "[import] 旧目录与新安装目录相同，无需导入。")
		return 1
	}
	if _, err := os.Stat(filepath.Join(newRoot, "artex.db")); err == nil {
		fmt.Fprintf(os.Stderr, "[import] %s 下已存在 artex.db（可能已导入过）。如需强制重新导入，请先删除新目录中的 artex.db。\n", newRoot)
		return 1
	}

	fmt.Printf("[import] 旧目录：%s\n[import] 新安装目录：%s\n\n", oldDir, newRoot)

	// ④ 数据库与密钥（必须成套：credential.key 加密的 LLM API key 没有它解不开）。
	if !importFile(oldDir, newRoot, "artex.db") {
		importFailed = append(importFailed, "artex.db")
		return 1 // 主库导入失败，后续没有意义
	}
	importSidecar(oldDir, newRoot, "artex.db-wal")
	importSidecar(oldDir, newRoot, "artex.db-shm")
	if !importFile(oldDir, newRoot, "credential.key") {
		importFailed = append(importFailed, "credential.key")
	}
	importOptionalFile(oldDir, newRoot, "jwt.key", "不拷贝只影响已登录会话，需重新登录")

	// ⑤ config.json：只保留 listen/language。database.path/skill_dir 若指向旧目录
	//    的绝对路径，带过来会让新应用继续读写旧目录。
	importConfigJSON(filepath.Join(oldDir, "config.json"), filepath.Join(newRoot, "config.json"))

	// ⑥ data/ 工作空间树（排除 .delete-staging 崩溃残留）。
	importTree(
		filepath.Join(oldDir, "data"),
		filepath.Join(newRoot, "data"),
		[]string{".delete-staging"},
	)

	// ⑦ skills 整树旧覆盖新：用户改动全保留；同名内置技能停在旧版快照，
	//    UI「恢复出厂」可逐个升到新版。新快照里旧快照没有的文件不受影响。
	importTree(
		filepath.Join(oldDir, "skills"),
		filepath.Join(newRoot, "skills"),
		nil,
	)

	// ⑧ 根目录的 i<意图ID> 产物目录 → 新 data/（best-effort：失败只记日志）。
	importLegacyRunDirs(oldDir, filepath.Join(newRoot, "data"))

	// ⑨ 汇总。
	if len(importFailed) > 0 {
		fmt.Fprintf(os.Stderr, "\n[import] 导入完成，但以下项失败：%s\n", strings.Join(importFailed, "、"))
		fmt.Fprintln(os.Stderr, "[import] 失败项可修复后单独重试（重新运行本命令，已成功的项会跳过或覆盖为同一内容）。")
		return 1
	}
	fmt.Printf("\n[import] 导入完成。启动 artex-hydra-desktop 即可沿用旧版的全部数据（首次启动会自动完成数据库迁移）。\n")
	return 0
}

// legacyInstallMarker 判断目录是否具备旧版安装的特征（入口 exe 或 versions 布局）。
func legacyInstallMarker(dir string) bool {
	for _, name := range []string{"artex-rea.exe", "artex-rea", "versions"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// portInUse 探测 127.0.0.1:<port> 是否有监听者。
func portInUse(port string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// importFile 拷贝单个必须存在的文件；成功输出清单，失败记入 importFailed。
func importFile(oldDir, newRoot, name string) bool {
	src := filepath.Join(oldDir, name)
	dst := filepath.Join(newRoot, name)
	if err := copyFile(src, dst); err != nil {
		fmt.Fprintf(os.Stderr, "[import] ✗ %s：%v\n", name, err)
		return false
	}
	info, _ := os.Stat(dst)
	fmt.Printf("[import] ✓ %-18s %s\n", name, humanSize(info.Size()))
	return true
}

// importOptionalFile 拷贝可选文件（缺失只提示，不算失败）。
func importOptionalFile(oldDir, newRoot, name, missingNote string) {
	src := filepath.Join(oldDir, name)
	if _, err := os.Stat(src); err != nil {
		fmt.Printf("[import] － %-18s 旧目录不存在（%s）\n", name, missingNote)
		return
	}
	if err := copyFile(src, filepath.Join(newRoot, name)); err != nil {
		fmt.Fprintf(os.Stderr, "[import] ✗ %s：%v\n", name, err)
		importFailed = append(importFailed, name)
		return
	}
	fmt.Printf("[import] ✓ %s\n", name)
}

// importSidecar 拷贝数据库的 WAL/SHM 伴生文件（只在旧目录残留时才存在；
// 干净退出的旧库只剩主文件）。
func importSidecar(oldDir, newRoot, name string) {
	src := filepath.Join(oldDir, name)
	if _, err := os.Stat(src); err != nil {
		return
	}
	if err := copyFile(src, filepath.Join(newRoot, name)); err != nil {
		fmt.Fprintf(os.Stderr, "[import] ✗ %s：%v\n", name, err)
		importFailed = append(importFailed, name)
		return
	}
	fmt.Printf("[import] ✓ %s\n", name)
}

// importConfigJSON 从旧 config.json 提取可安全迁移的字段写入新安装目录。
// database.path/skill_dir 指向旧目录的绝对路径不迁移——否则新应用会继续读写旧目录。
func importConfigJSON(src, dst string) {
	raw, err := os.ReadFile(src)
	if err != nil {
		fmt.Printf("[import] － config.json   旧目录不存在（新安装使用默认配置）\n")
		return
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		fmt.Fprintf(os.Stderr, "[import] ✗ config.json 不是合法 JSON，跳过（新安装使用默认配置）：%v\n", err)
		importFailed = append(importFailed, "config.json")
		return
	}
	safe := map[string]any{}
	for _, key := range []string{"listen", "language"} {
		if v, ok := m[key]; ok {
			safe[key] = v
		}
	}
	out, err := json.MarshalIndent(safe, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[import] ✗ config.json 序列化失败：%v\n", err)
		importFailed = append(importFailed, "config.json")
		return
	}
	if err := os.WriteFile(dst, append(out, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "[import] ✗ 写入 config.json 失败：%v\n", err)
		importFailed = append(importFailed, "config.json")
		return
	}
	fmt.Printf("[import] ✓ config.json（仅迁移 listen/language）\n")
}

// importTree 递归把 src 树拷到 dst（merge 语义：同名覆盖、dst 独有保留；
// skip 列出 src 顶层要排除的名字）。dst 不存在自动创建。
func importTree(src, dst string, skip []string) {
	if _, err := os.Stat(src); err != nil {
		fmt.Printf("[import] － %-18s 旧目录不存在\n", filepath.Base(src))
		return
	}
	skipSet := map[string]bool{}
	for _, name := range skip {
		skipSet[name] = true
	}
	files := 0
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if skipSet[filepath.Base(rel)] && d.IsDir() && !strings.Contains(rel, string(filepath.Separator)) {
			return fs.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		files++
		return nil
	})
	label := filepath.Base(src)
	if label == "data" {
		label = "data/（工作空间树）"
	} else if label == "skills" {
		label = "skills/（整树旧覆盖新）"
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "[import] ✗ %s：%v\n", label, err)
		importFailed = append(importFailed, label)
		return
	}
	fmt.Printf("[import] ✓ %-18s %d 个文件\n", label, files)
}

// importLegacyRunDirs 把旧安装根目录的 i<意图ID> 产物目录拷进新 data/
// （旧版 worker 在 workDir 为空时回退进程 CWD，这些目录因此落在安装根）。
func importLegacyRunDirs(oldDir, newDataDir string) {
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return
	}
	moved := 0
	for _, entry := range entries {
		if !entry.IsDir() || !legacyRunDirRe.MatchString(entry.Name()) {
			continue
		}
		src := filepath.Join(oldDir, entry.Name())
		dst := filepath.Join(newDataDir, entry.Name())
		if err := copyTreeBestEffort(src, dst); err != nil {
			fmt.Fprintf(os.Stderr, "[import] ✗ %s：%v\n", entry.Name(), err)
			importFailed = append(importFailed, entry.Name())
			continue
		}
		moved++
	}
	if moved > 0 {
		fmt.Printf("[import] ✓ %-18s %d 个产物目录（i<意图ID>）\n", "i<id>/ 产物目录", moved)
	}
}

// copyTreeBestEffort 是不带清单输出的整树拷贝（用于 best-effort 项）。
func copyTreeBestEffort(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// copyFile 全量拷贝单个文件（覆盖目标）。目录自动创建。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	// 保留源文件的修改时间： transcripts 等按时间浏览的场景依赖它。
	if info, statErr := os.Stat(src); statErr == nil {
		_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	return nil
}

// humanSize 把字节数转成人类可读形式。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
