package server

// Camoufox 自动化浏览器设置。
//
// 能力来源：Camoufox 是基于 Firefox 源码构建的指纹伪装浏览器，原生支持
// Playwright 风格的 proxy 参数，并且当 geoip=True 时自动按出口 IP 对齐时区、
// 语言、WebRTC IP，是当前对抗 Cloudflare 等强风控场景下开源界的最强方案。
//
// 三条设计原则（与 shell_settings.go 一致）：
//  1. 探测不写库，只有用户显式点「重新检测」或「一键安装」时才更新；
//  2. 检测覆盖全盘多目录（用户可能用不同 Python 环境装到不同位置）；
//  3. 安装与运行都继承全局出口代理（global_proxy）——Camoufox 过 Cloudflare 时
//     必须用同一出口 IP，否则 Turnstile 会因为 IP 与指纹不符而失败。

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	settingCamoufoxPath      = "camoufox_path"
	settingCamoufoxVersion   = "camoufox_version"
	settingCamoufoxInstalled = "camoufox_installed"
)

// camoufoxInstallMu 保证同一时刻只有一次安装在执行，避免用户连点。
var camoufoxInstallMu sync.Mutex

// camoufoxInstallLog 是最近一次安装的末尾日志，供前端轮询显示。
var camoufoxInstallLog = &installLog{}

type installLog struct {
	mu    sync.Mutex
	lines []string
}

func (c *installLog) append(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.lines) >= 200 {
		c.lines = c.lines[len(c.lines)-199:]
	}
	c.lines = append(c.lines, line)
}

func (c *installLog) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 返回空 slice 而非 nil：JSON 序列化时 nil → null，前端 CamoufoxSettings
	// 声明的是 string[]，收到 null 会在 .length / .join 上崩。
	if len(c.lines) == 0 {
		return []string{}
	}
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return out
}

func (c *installLog) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = nil
}

// camoufoxCandidates 是所有可能的安装路径模板（%HOME% 由实际用户目录替换）。
// 覆盖 Windows / macOS / Linux 三大平台的默认缓存位置，以及常见的 site-packages
// 布局，避免用户装在不同 Python 环境时检测不到。
func camoufoxCandidates() []string {
	home, _ := os.UserHomeDir()
	cands := []string{}

	if home != "" {
		cands = append(cands,
			filepath.Join(home, "AppData", "Local", "camoufox", "camoufox", "Cache", "camoufox.exe"),
			filepath.Join(home, "AppData", "Local", "camoufox", "camoufox", "Cache", "camoufox"),
			filepath.Join(home, "AppData", "Roaming", "camoufox", "camoufox", "Cache", "camoufox.exe"),
			filepath.Join(home, "AppData", "Roaming", "camoufox", "camoufox", "Cache", "camoufox"),
			filepath.Join(home, "Library", "Caches", "camoufox", "camoufox", "camoufox"),
			filepath.Join(home, ".cache", "camoufox", "camoufox", "camoufox"),
			filepath.Join(home, "AppData", "Local", "ms-playwright", "camoufox", "camoufox.exe"),
		)
	}

	// 自定义安装位置兜底：用户可能把 Camoufox 装在非默认目录（如 E:\Camoufox）。
	// 检测时按 Windows 盘符 C-F 全扫，路径结构必须是 camoufox\Cache\browsers\*
	// 下的版本目录。这里用 WalkDir 太慢；改用 Windows 下「盘符根\Camoufox」直查
	// + 标准 Cache 结构推导，兼顾速度与覆盖率。
	if runtime.GOOS == "windows" {
		for _, root := range []string{"C:\\Camoufox", "D:\\Camoufox", "E:\\Camoufox", "F:\\Camoufox"} {
			if st, err := os.Stat(root); err != nil || !st.IsDir() {
				continue
			}
			// 常见两种布局：<root>\camoufox.exe（扁平解压）或 <root>\camoufox\Cache\browsers\*\<ver>\camoufox.exe
			cands = append(cands,
				filepath.Join(root, "camoufox.exe"),
				filepath.Join(root, "camoufox", "Cache", "camoufox.exe"),
			)
			// 扫描 <root>\camoufox\Cache\browsers\<repo>\<ver>\camoufox.exe
			browsersRoot := filepath.Join(root, "camoufox", "Cache", "browsers")
			if entries, err := os.ReadDir(browsersRoot); err == nil {
				for _, repo := range entries {
					if !repo.IsDir() {
						continue
					}
					repoDir := filepath.Join(browsersRoot, repo.Name())
					vers, err := os.ReadDir(repoDir)
					if err != nil {
						continue
					}
					for _, ver := range vers {
						if !ver.IsDir() {
							continue
						}
						cands = append(cands, filepath.Join(repoDir, ver.Name(), "camoufox.exe"))
					}
				}
			}
		}
	}

	if runtime.GOOS == "windows" {
		roots := []string{"C:\\", "D:\\", "E:\\", "F:\\"}
		pyNames := []string{"Python39", "Python310", "Python311", "Python312", "Python313"}
		for _, root := range roots {
			for _, py := range pyNames {
				base := filepath.Join(root, py)
				if st, err := os.Stat(base); err != nil || !st.IsDir() {
					continue
				}
				cands = append(cands,
					filepath.Join(base, "Lib", "site-packages", "camoufox", "camoufox", "camoufox.exe"),
					filepath.Join(base, "Lib", "site-packages", "camoufox", "camoufox", "camoufox"),
				)
			}
		}
	}

	if v := strings.TrimSpace(os.Getenv("CAMOUFOX_HOME")); v != "" {
		cands = append(cands,
			filepath.Join(v, "camoufox.exe"),
			filepath.Join(v, "camoufox"),
			filepath.Join(v, "camoufox", "camoufox"),
		)
	}

	return cands
}

// detectCamoufox 返回本机是否已安装 Camoufox 以及其可执行文件路径。
func detectCamoufox(python string) (path string, version string) {
	if python != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, python, "-m", "camoufox", "path")
		if out, err := cmd.Output(); err == nil {
			p := strings.TrimSpace(string(out))
			if _, err := os.Stat(p); err == nil {
				return p, camoufoxVersionFromCLI(python)
			}
		}
	}

	for _, c := range camoufoxCandidates() {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, ""
		}
	}

	return "", ""
}

func camoufoxVersionFromCLI(python string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-m", "camoufox", "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	parts := strings.Fields(line)
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return line
}

// camoufoxSettingsPayload 是 GET /api/settings/camoufox 的响应体。
func (s *Server) camoufoxSettingsPayload() map[string]any {
	python := s.pythonInterpreter()
	path, version := detectCamoufox(python)
	installed := path != ""
	proxy := s.m.GlobalProxy()

	// geoip extra 装的是 geoip2（MaxMind 的 Python 库），不是 camoufox 自身；
	// 检查 geoip2 才能准确判断是否装了 [geoip] extra。
	solving := map[string]any{
		"pylcaptcha": camoufoxPythonPackageReady(python, "pylcaptcha"),
		"geoip":      camoufoxPythonPackageReady(python, "geoip2"),
	}

	return map[string]any{
		"installed":          installed,
		"path":               path,
		"version":            version,
		"python":             python,
		"proxy":              proxy,
		"solving_libs":       solving,
		"recent_install_log": camoufoxInstallLog.snapshot(),
		"cli":                s.camoufoxCLISummary(),
	}
}

// camoufoxPythonPackageReady 判断某个 Python 包是否已安装。
//
// 关键：绝不能用 `import <pkg>` 来做检测。pylcaptcha 在 import 阶段就会
// 触发 Hugging Face 上的 YOLO 模型下载（100+MB），在国内网络会卡死好几分钟，
// 设置页点一次"重新检测"就相当于卡一次。
//
// 这里改用 importlib.metadata（纯读 dist-info 元数据，不执行任何模块代码），
// 只判断"这个包在 pip list 里存不存在"，不关心它能不能完整初始化。
func camoufoxPythonPackageReady(python, pkg string) bool {
	if python == "" || pkg == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code := "import importlib.metadata as m, sys\n" +
		"try:\n" +
		"    m.version(sys.argv[1])\n" +
		"except m.PackageNotFoundError:\n" +
		"    sys.exit(1)\n"
	cmd := exec.CommandContext(ctx, python, "-c", code, pkg)
	return cmd.Run() == nil
}

// pgGetCamoufoxSettings 是 GET /api/settings/camoufox 的 handler。
func (s *Server) pgGetCamoufoxSettings(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	writeJSON(w, 200, s.camoufoxSettingsPayload())
}

// pgDetectCamoufox 是 POST /api/settings/camoufox/detect 的 handler。
func (s *Server) pgDetectCamoufox(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	writeJSON(w, 200, s.camoufoxSettingsPayload())
}

// pgInstallCamoufox 是 POST /api/settings/camoufox/install 的 handler。
//
// 分两步执行：
//  1. pip install camoufox[geoip] pylcaptcha；
//  2. python -m camoufox fetch。
//
// 代理贯穿：若用户设置了全局出口代理，安装命令会通过 HTTP_PROXY/HTTPS_PROXY
// 环境变量继承，避免下载被墙或走错出口。
func (s *Server) pgInstallCamoufox(w http.ResponseWriter, r *http.Request) {
	noStore(w)

	python := s.pythonInterpreter()
	if python == "" {
		writeErr(w, 400, "未检测到 Python 解释器，请先在设置里配置 Python 路径")
		return
	}

	camoufoxInstallMu.Lock()
	defer camoufoxInstallMu.Unlock()

	camoufoxInstallLog.reset()
	proxy := s.m.GlobalProxy()

	runWithProxy := func(name string, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, python, args...)
		cmd.Env = os.Environ()
		if proxy != "" {
			cmd.Env = append(cmd.Env,
				"HTTP_PROXY="+proxy,
				"HTTPS_PROXY="+proxy,
				"ALL_PROXY="+proxy,
			)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		go streamScan(name, stdout)
		if stderr != nil {
			go streamScan(name, stderr)
		}
		return cmd.Wait()
	}

	if err := runWithProxy("pip-install", []string{"-m", "pip", "install", "-U", "camoufox[geoip]", "pylcaptcha"}); err != nil {
		writeErr(w, 500, "pip install 失败："+err.Error())
		return
	}

	if err := runWithProxy("camoufox-fetch", []string{"-m", "camoufox", "fetch"}); err != nil {
		writeErr(w, 500, "camoufox fetch 失败："+err.Error())
		return
	}

	path, version := detectCamoufox(python)
	installed := path != ""
	if err := s.m.pg.SetSetting(settingCamoufoxPath, path); err != nil {
		log.Printf("[camoufox] 写 path 设置失败：%v", err)
	}
	if err := s.m.pg.SetSetting(settingCamoufoxVersion, version); err != nil {
		log.Printf("[camoufox] 写 version 设置失败：%v", err)
	}
	installedStr := "false"
	if installed {
		installedStr = "true"
	}
	_ = s.m.pg.SetSetting(settingCamoufoxInstalled, installedStr)

	writeJSON(w, 200, map[string]any{
		"installed": installed,
		"path":      path,
		"version":   version,
	})
}

// streamScan 逐行读取命令输出并写入安装日志缓冲区。
func streamScan(prefix string, r interface{ Read([]byte) (int, error) }) {
	if r == nil {
		return
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1*1024*1024)
	for sc.Scan() {
		camoufoxInstallLog.append(prefix + ": " + sc.Text())
	}
}

// ---------- Camoufox CLI（camoufox-browser） ----------
//
// CLI 能力：open / snapshot / click / fill / press / select / text / eval /
// screenshot / network / console / tabs / dialog / wait 等，是 AI 做浏览器
// 交互的唯一出口（保持 25 工具面不变，通过 Bash 调用 CLI）。
//
// 检测优先级：PATH 上的 camoufox-browser / camoufox-cli / camoufox →
// 全盘常见路径（Python Scripts 目录、盘符根\Camoufox\cli|bin）。

const (
	settingCamoufoxCLIPath    = "camoufox_cli_path"
	settingCamoufoxCLIVersion = "camoufox_cli_version"
)

// camoufoxCLICandidates 返回 CLI 可执行文件的候选路径。
func camoufoxCLICandidates() []string {
	home, _ := os.UserHomeDir()
	cands := []string{}

	if runtime.GOOS == "windows" {
		roots := []string{"C:\\", "D:\\", "E:\\", "F:\\"}
		pyNames := []string{"Python39", "Python310", "Python311", "Python312", "Python313"}
		for _, root := range roots {
			for _, py := range pyNames {
				base := filepath.Join(root, py, "Scripts")
				if st, err := os.Stat(base); err != nil || !st.IsDir() {
					continue
				}
				cands = append(cands,
					filepath.Join(base, "camoufox-browser.exe"),
					filepath.Join(base, "camoufox-browser"),
					filepath.Join(base, "camoufox-cli.exe"),
					filepath.Join(base, "camoufox-cli"),
					filepath.Join(base, "camoufox.exe"),
					filepath.Join(base, "camoufox"),
				)
			}
		}
	}

	if home != "" {
		cands = append(cands,
			filepath.Join(home, "AppData", "Local", "Programs", "Python", "Python313", "Scripts", "camoufox-browser.exe"),
			filepath.Join(home, "AppData", "Local", "Programs", "Python", "Python312", "Scripts", "camoufox-browser.exe"),
			filepath.Join(home, "AppData", "Local", "Programs", "Python", "Python311", "Scripts", "camoufox-browser.exe"),
			filepath.Join(home, "AppData", "Roaming", "npm", "camoufox-cli.cmd"),
			filepath.Join(home, "AppData", "Roaming", "npm", "camoufox-browser.cmd"),
			filepath.Join(home, ".local", "bin", "camoufox-browser"),
			filepath.Join(home, ".local", "bin", "camoufox-cli"),
			filepath.Join(home, "Library", "Python", "3.13", "bin", "camoufox-browser"),
		)
	}

	if runtime.GOOS == "windows" {
		for _, root := range []string{"C:\\Camoufox", "D:\\Camoufox", "E:\\Camoufox", "F:\\Camoufox"} {
			if st, err := os.Stat(root); err != nil || !st.IsDir() {
				continue
			}
			cands = append(cands,
				filepath.Join(root, "cli", "camoufox-browser.exe"),
				filepath.Join(root, "cli", "camoufox-cli.exe"),
				filepath.Join(root, "bin", "camoufox-browser.exe"),
				filepath.Join(root, "bin", "camoufox-cli.exe"),
				filepath.Join(root, "camoufox-browser.exe"),
				filepath.Join(root, "camoufox-cli.exe"),
			)
		}
	}

	return cands
}

// detectCamoufoxCLI 返回本机已安装的 CLI 路径与版本号（可能为空）。
func detectCamoufoxCLI() (path string, version string) {
	for _, name := range []string{"camoufox-browser", "camoufox-cli", "camoufox"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, camoufoxCLIVersion(p)
		}
	}
	for _, c := range camoufoxCLICandidates() {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, camoufoxCLIVersion(c)
		}
	}
	return "", ""
}

// camoufoxCLIVersion 执行 <cli> --version，解析末段作为版本号。
func camoufoxCLIVersion(cliPath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliPath, "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	parts := strings.Fields(line)
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return line
}

// camoufoxCLISummary 汇总给前端展示的 CLI 状态。
func (s *Server) camoufoxCLISummary() map[string]any {
	path, version := detectCamoufoxCLI()
	return map[string]any{
		"installed": path != "",
		"path":      path,
		"version":   version,
	}
}

// pgInstallCamoufoxCLI 是 POST /api/settings/camoufox/cli/install 的 handler。
// 通过 pip install camoufox-browser 安装 CLI，安装完成后重新探测。
func (s *Server) pgInstallCamoufoxCLI(w http.ResponseWriter, r *http.Request) {
	noStore(w)

	python := s.pythonInterpreter()
	if python == "" {
		writeErr(w, 400, "未检测到 Python 解释器，请先在设置里配置 Python 路径")
		return
	}

	camoufoxInstallMu.Lock()
	defer camoufoxInstallMu.Unlock()

	camoufoxInstallLog.reset()
	proxy := s.m.GlobalProxy()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-m", "pip", "install", "-U", "camoufox-browser")
	cmd.Env = os.Environ()
	if proxy != "" {
		cmd.Env = append(cmd.Env,
			"HTTP_PROXY="+proxy,
			"HTTPS_PROXY="+proxy,
			"ALL_PROXY="+proxy,
		)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeErr(w, 500, "启动 pip 失败："+err.Error())
		return
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		writeErr(w, 500, "启动 pip 失败："+err.Error())
		return
	}
	go streamScan("camoufox-cli-pip", stdout)
	if stderr != nil {
		go streamScan("camoufox-cli-pip", stderr)
	}
	if err := cmd.Wait(); err != nil {
		writeErr(w, 500, "camoufox-browser 安装失败："+err.Error())
		return
	}

	path, version := detectCamoufoxCLI()
	if path != "" {
		_ = s.m.pg.SetSetting(settingCamoufoxCLIPath, path)
		_ = s.m.pg.SetSetting(settingCamoufoxCLIVersion, version)
	}
	writeJSON(w, 200, map[string]any{
		"installed": path != "",
		"path":      path,
		"version":   version,
	})
}

// pgDetectCamoufoxCLI 是 POST /api/settings/camoufox/cli/detect 的 handler（只读）。
func (s *Server) pgDetectCamoufoxCLI(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	path, version := detectCamoufoxCLI()
	writeJSON(w, 200, map[string]any{
		"installed": path != "",
		"path":      path,
		"version":   version,
	})
}
