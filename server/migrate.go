package server

import (
	"context"
	"encoding/json"
	"net/http"
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
		"candidates":   migrate.Scan(root, nil),
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
	c, err := migrate.Inspect(path)
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

	res, err := migrate.Import(ctx, s.m.PG().DB, config.BaseDir(), path, mode)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
