// Package migrate 负责「旧安装目录 → 当前安装目录」的体检与导入。
//
// 为什么两端共用：桌面端（artex-hydra-desktop，单文件）与服务器版（artex-rea，
// 入口 + 核心）的安装目录布局完全一致——artex.db + credential.key + jwt.key +
// data/ + skills/ ——所以同一套逻辑既能从 WEB 端搬到桌面端，也能反过来搬。
//
// 分工：本包只做"看"和"搬"，不碰 HTTP、不碰 UI、不假定自己跑在哪个壳里；
// 调用方（server 的 /api/migrate/* 与桌面端的 -import-old）负责路径来源与权限。
package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 候选目录扫描范围。改动这里要同步 web 端文案（migrate-card.tsx）。
const (
	// candidateRootsEnv 允许额外追加扫描根目录（os.PathListSeparator 分隔）。
	candidateRootsEnv = "ARTEX_MIGRATE_ROOTS"
	// maxScanDepth 从每个根往下看几层：桌面 → ARTEX程序 是 1 层，再深就是用户的
	// 其它目录，扫进去既慢又容易误判。
	maxScanDepth = 2
	// runningProbeTimeout 判断"那个目录是不是正在运行"的端口探测超时。
	runningProbeTimeout = 300 * time.Millisecond
)

// probePorts 是 ARTEX 自己会占的端口：主服务 8787、流量记录代理 8788。
// 任一在监听就认为"旧版可能还在跑"，与 import-old 的 CLI 判据保持一致。
var probePorts = []int{8787, 8788}

// TableCount 是单表的行数，用于体检展示与导入结果对账。
type TableCount struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// Candidate 是一个候选的旧安装目录。
//
// 字段全是展示用的事实，不做任何"建议迁移"的判断——判据留给调用方与用户。
type Candidate struct {
	Path string `json:"path"`
	// Kind: "web"（artex-rea 两件套）/ "desktop"（artex-hydra-desktop 单文件）/ "unknown"。
	Kind string `json:"kind"`
	// Version 取自 pointer.json 的 current；单文件发行没有 pointer，留空。
	Version string `json:"version"`
	// HasDB 是这个目录值得迁移的唯一硬条件。
	HasDB       bool  `json:"has_db"`
	DBSizeBytes int64 `json:"db_size_bytes"`
	// Tables 只包含有数据的表（行数 > 0），按名字排序。
	Tables    []TableCount `json:"tables"`
	TotalRows int64        `json:"total_rows"`

	HasCredentialKey bool `json:"has_credential_key"`
	HasJwtKey        bool `json:"has_jwt_key"`

	// DataEntries / SkillEntries 是一层条目数，够用户判断"里面有没有干货"。
	DataEntries  int `json:"data_entries"`
	SkillEntries int `json:"skill_entries"`

	// Running 表示本机 8787/8788 有人在监听，只提示风险，不阻断展示。
	Running bool `json:"running"`
	// IsCurrent 表示这个目录就是当前进程的安装目录（不能从自己导入自己）。
	IsCurrent bool `json:"is_current"`
	// Note 是给用户看的一句话结论（例如"没有 artex.db，跳过"）。
	Note string `json:"note,omitempty"`
}

// Inspect 体检单个目录。
//
// 目录不存在、没有 artex.db 都返回错误；能打开但没有表的库返回 HasDB=true 且
// Tables 为空——那是"空库"，用户自己判断。
func Inspect(dir string) (Candidate, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Candidate{}, fmt.Errorf("解析路径失败：%w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return Candidate{}, fmt.Errorf("目录不可访问：%w", err)
	}
	if !st.IsDir() {
		return Candidate{}, fmt.Errorf("%s 不是目录", abs)
	}

	c := Candidate{Path: abs, Kind: detectKind(abs)}
	c.Version = pointerVersion(abs)
	c.HasCredentialKey = fileExists(filepath.Join(abs, "credential.key"))
	c.HasJwtKey = fileExists(filepath.Join(abs, "jwt.key"))
	c.DataEntries = countEntries(filepath.Join(abs, "data"))
	c.SkillEntries = countEntries(filepath.Join(abs, "skills"))
	c.Running = portsListening(probePorts)

	dbPath := filepath.Join(abs, "artex.db")
	if fi, err := os.Stat(dbPath); err == nil {
		c.HasDB = true
		c.DBSizeBytes = fi.Size()
		tables, total, terr := countTables(dbPath)
		if terr != nil {
			c.Note = "数据库打不开（可能被占用或已损坏）：" + terr.Error()
		} else {
			c.Tables = tables
			c.TotalRows = total
			if total == 0 {
				c.Note = "数据库可读但没有数据"
			}
		}
	} else {
		c.Note = "没有 artex.db，不像 ARTEX 安装目录"
	}
	return c, nil
}

// Scan 扫描候选目录，返回"值得看一眼"的目录（必须含 artex.db）。
//
// roots 为空时用默认范围：用户主目录下的 Desktop / Downloads / Documents，加上
// 当前安装目录的同级目录——旧版通常就解压在桌面或新目录旁边。
func Scan(currentRoot string, roots []string) []Candidate {
	seen := map[string]bool{}
	var out []Candidate
	roots = append(roots, defaultRoots(currentRoot)...)
	for _, extra := range strings.Split(os.Getenv(candidateRootsEnv), string(os.PathListSeparator)) {
		if strings.TrimSpace(extra) != "" {
			roots = append(roots, extra)
		}
	}

	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		for _, dir := range walkDirs(root, maxScanDepth) {
			abs, aerr := filepath.Abs(dir)
			if aerr != nil || seen[abs] {
				continue
			}
			if !fileExists(filepath.Join(abs, "artex.db")) {
				continue
			}
			seen[abs] = true
			c, ierr := Inspect(abs)
			if ierr != nil {
				continue
			}
			if currentRoot != "" {
				if cur, cerr := filepath.Abs(currentRoot); cerr == nil && samePath(cur, abs) {
					c.IsCurrent = true
				}
			}
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TotalRows > out[j].TotalRows })
	return out
}

// defaultRoots 是"旧版最可能躺在这里"的固定范围，刻意保守：只到用户目录的一层
// 子目录，避免扫全盘（既慢又会把各种无关 sqlite 库当候选）。
func defaultRoots(currentRoot string) []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, sub := range []string{"Desktop", "Downloads", "Documents"} {
			roots = append(roots, filepath.Join(home, sub))
		}
	}
	if currentRoot != "" {
		roots = append(roots, filepath.Dir(filepath.Clean(currentRoot)))
	}
	return roots
}

// walkDirs 列出 root 下 1..depth 层的子目录（不含 root 自身）。
func walkDirs(root string, depth int) []string {
	var out []string
	var walk func(dir string, left int)
	walk = func(dir string, left int) {
		if left == 0 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") || strings.EqualFold(name, "node_modules") {
				continue
			}
			full := filepath.Join(dir, name)
			out = append(out, full)
			walk(full, left-1)
		}
	}
	walk(root, depth)
	return out
}

// detectKind 按目录里的可执行文件判断这条数据是哪个形态产出来的。
func detectKind(dir string) string {
	for _, name := range []string{"artex-hydra-desktop.exe", "artex-hydra-desktop", "artex-hydra.exe", "artex-hydra"} {
		if fileExists(filepath.Join(dir, name)) {
			return "desktop"
		}
	}
	for _, name := range []string{"artex-rea.exe", "artex-rea", "artex-rea-core.exe", "artex-rea-core"} {
		if fileExists(filepath.Join(dir, name)) || fileExists(filepath.Join(dir, "versions")) {
			return "web"
		}
	}
	return "unknown"
}

// pointerVersion 读 pointer.json 的 current；单文件发行没有该文件，返回空。
func pointerVersion(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "pointer.json"))
	if err != nil {
		return ""
	}
	var p struct {
		Current string `json:"current"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	return strings.TrimSpace(p.Current)
}

// countTables 只读打开旧库，返回有数据的表与总行数。
//
// 用附件式只读 URI 打开，绝不写旧目录：迁移的前提是"旧目录当作只读证据"。
func countTables(dbPath string) ([]TableCount, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	db, err := sql.Open("sqlite", readOnlyDSN(dbPath))
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, 0, err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		return nil, 0, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Err()
			return nil, 0, err
		}
		if strings.HasPrefix(name, "sqlite_") {
			continue
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, err
	}
	_ = rows.Close()

	var out []TableCount
	var total int64
	for _, name := range names {
		var n int64
		// 表名来自 sqlite_master，不是用户输入；仍走引号包裹，避免保留字撞车。
		q := fmt.Sprintf(`SELECT count(*) FROM "%s"`, strings.ReplaceAll(name, `"`, `""`))
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			continue // 视图/虚拟表读不了就跳过，不影响整体体检
		}
		if n == 0 {
			continue
		}
		out = append(out, TableCount{Name: name, Rows: n})
		total += n
	}
	return out, total, nil
}

// readOnlyDSN 拼一个只读 URI。Windows 路径里的反斜杠统一换成斜杠。
func readOnlyDSN(path string) string {
	p := filepath.ToSlash(path)
	// file: 开头 + ?mode=ro：只读打开，旧目录一个字节都不会被改。
	return "file:" + p + "?mode=ro"
}

// portsListening 探测本机端口是否有监听者。
func portsListening(ports []int) bool {
	for _, p := range ports {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), runningProbeTimeout)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func countEntries(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		n++
	}
	return n
}

// samePath 比较两个绝对路径是否指向同一处（Windows 大小写不敏感）。
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
