# TUI HARNESS 代码归档

本目录保存**已暂停开发**的终端 TUI HARNESS 全部源码。它不在构建、测试、编译
或任何 CI 流程中——目录名以下划线开头（`_archive`），Go 工具链会整体忽略它。

## 为什么归档

TUI 的主要难点不在后端，而在前端设计：全屏终端界面要重做一套与 Web 控制台
等价的交互（活动流、漏洞、任务、输入、响应式布局），投入产出比低于直接把
Web 对话页做好。因此暂停 TUI，恢复 Web 对话页，把精力放回浏览器端。

代码完整保留，不删除：后端适配层、渲染层与测试都在，随时可以接回来。

## 归档内容

| 归档路径 | 原路径 | 说明 |
| --- | --- | --- |
| `tui/` | `tui/` | TUI 包本体（7 个文件）：主题、后端接口、状态机、渲染、命令、入口、单测 |
| `server/tui.go` | `server/tui.go` | `TUISession`，把 Server 的进程内能力实现为 `tui.TUIBackend` |
| `cmd/artex/tuicmd.go` | `cmd/artex/tuicmd.go` | `artex tui` 子命令：安装目录守卫、日志重定向、装配与退出码 |

## 恢复步骤

1. 把三个路径原样拷回：

   ```bash
   mkdir -p tui
   cp _archive/tui-harness/tui/*.go tui/
   cp _archive/tui-harness/server/tui.go server/
   cp _archive/tui-harness/cmd/artex/tuicmd.go cmd/artex/
   ```

2. 恢复 `cmd/artex/main.go` 的子命令分发（在 `mcp` 分支之前）：

   ```go
   if len(os.Args) > 1 && os.Args[1] == "tui" {
       return runTUICommand(os.Args[2:])
   }
   ```

3. 恢复依赖（归档期间它们已被 `go mod tidy` 移除）：

   ```bash
   go get github.com/charmbracelet/bubbletea@v1.3.10 \
          github.com/charmbracelet/bubbles@v1.0.0 \
          github.com/charmbracelet/lipgloss@v1.1.0
   ```

4. 恢复入口默认行为（可选）：`cmd/artex-launcher/main.go` 中把无参默认从
   Web 控制台改回 `tui` 子命令。

`boot.Options.Headless` 一直保留在 `boot/boot.go` 里，无需恢复——它就是为
TUI 准备的"装配但不监听端口"开关。

## 当前替代入口

Web 控制台的对话页（`/chat`）已恢复，是唯一的人机交互入口：

```bash
./start.sh          # Linux / macOS
start.bat           # Windows
```
