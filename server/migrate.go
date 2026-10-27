package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/migrate"
)

// 数据迁移：把另一份 ARTEX 安装目录（WEB 版 ⇄ 桌面端）的数据库与工作空间搬进来。
//
// 为什么放在 HTTP 层而不是只留 CLI：用户手上往往是"旧版解压在桌面、新版也在桌面"
// 这种最朴素的场景，让他去开 CMD 敲绝对路径才是真正的门槛。窗口内与浏览器里
// 看到的是同一张卡片（前端只有一份），所以桌面端与 WEB 端天然都能用。
//
// 权限沿用默认 JWT 鉴权（auth.go 只放行 /api/auth/* 与 /api/health）：窗口请求
// 带进程内标记直接过，浏览器侧要先登录。它改的是本机磁盘上的数据，这一点必须
// 守住——所以不开放匿名。

// migrateConfirmWord 是 replace 模式必须回传的确认词。写错/不写直接 400：
// 这是唯一会清空当前库的操作，宁可让人多打一次字。
const migrateConfirmWord = "REPLACE"

// migrateTimeout 给一次导入留足时间：29MB 的库 + 20MB 技能树在不慢的机器上
// 约 10~30 秒，但机械硬盘 + 杀软扫描能拖到几分钟。
const migrateTimeout = 10 * time.Minute

var migrateMu sync.Mutex

// migrateScan 扫描常见位置，返回候选目录（GET /api/migrate/scan）。
func (s *Server) migrateScan(w http.ResponseWriter, r *http.Request) {
	root := config.BaseDir()
	writeJSON(w, http.StatusOK, map[string]any{
		"current_root": root,
		"candidates":   migrate.Scan(root, nil, s.migrateOptions()),
	})
}

// migrateInspect 体检一个指定目录（POST /api/migrate/inspect {path}）。
func (s *Server) migrateInspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		writeErr(w, http.StatusBadRequest, "缺少 path")
		return
	}
	c, err := migrate.Inspect(path, s.migrateOptions())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 顺手标出"这个就是当前安装目录"——前端据此把该行禁掉。
	if cur := config.BaseDir(); cur != "" && strings.EqualFold(strings.TrimRight(c.Path, `\/`), strings.TrimRight(cur, `\/`)) {
		c.IsCurrent = true
	}
	writeJSON(w, http.StatusOK, c)
}

// migrateImport 执行导入（POST /api/migrate/import {path, mode, confirm}）。
//
// 同步执行：导入完成才返回，前端据此展示逐表结果。中途不让第二个请求插进来
// （migrateMu），否则两个导入会互相 DELETE/INSERT 同一批表。
func (s *Server) migrateImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		writeErr(w, http.StatusBadRequest, "缺少 path")
		return
	}
	mode := migrate.ParseMode(req.Mode)
	if mode == migrate.ModeReplace && strings.TrimSpace(req.Confirm) != migrateConfirmWord {
		writeErr(w, http.StatusBadRequest, "替换模式需要确认词 "+migrateConfirmWord+"：它会把当前数据库的每张表先清空（清空前已自动备份）")
		return
	}
	if !migrateMu.TryLock() {
		writeErr(w, http.StatusConflict, "已有导入在进行")
		return
	}
	defer migrateMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), migrateTimeout)
	defer cancel()

	res, err := migrate.Import(ctx, s.m.PG().DB, config.BaseDir(), path, mode, s.migrateOptions())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// migrateOptions 组装体检/导入要用的口径：把本进程自己占着的端口报给 migrate。
//
// 8787/8788 的探测本来是给"服务启动之前跑的 -import-old"用的；从界面里发起导入时
// 主服务与流量记录代理由本进程自己占着，不排除就会永远判"旧版还在跑"，导入发不出去。
func (s *Server) migrateOptions() migrate.Options {
	var own []int
	add := func(addr string) {
		if p := migratePortOf(addr); p > 0 {
			own = append(own, p)
		}
	}
	add(s.ListenAddr())
	// 流量记录代理只要起来了就一直在监听着自己的端口（capture 开关只决定要不要把
	// 代理注入给 agent，不影响监听），所以按"代理实例是否存在"判断；它没起来时
	// m.traffic 为 nil，端口不归我们，照常探测。
	if t := s.m.Traffic(); t != nil {
		add(t.ProxyAddr())
	}
	return migrate.Options{OwnPorts: own}
}

// migratePortOf 取端口：能处理监听地址（"127.0.0.1:8787" / ":8787" / "8787"）与
// 代理 URL（traffic.ProxyAddr() 给的是 "http://127.0.0.1:8788"）。取不到返回 0。
func migratePortOf(addr string) int {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return 0
	}
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return migratePortNum(p)
	}
	if u, uerr := url.Parse(addr); uerr == nil && u.Host != "" {
		if _, p, err := net.SplitHostPort(u.Host); err == nil {
			return migratePortNum(p)
		}
		return 0
	}
	return migratePortNum(addr) // 裸端口："8787"
}

// migratePortNum 收敛成合法端口号；非法一律 0（调用方按"不是我们的端口"处理）。
func migratePortNum(p string) int {
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 || n > 65535 {
		return 0
	}
	return n
}
