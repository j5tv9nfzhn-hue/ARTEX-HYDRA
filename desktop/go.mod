module github.com/j5tv9nfzhn-hue/artex-desktop

go 1.26.3

require (
	github.com/Autumn-27/artex v0.0.0
	github.com/wailsapp/wails/v2 v2.17.0
)

// 桌面壳复用根模块的全部后端包（server / boot / config / selfupdate / agent…）。
// 独立模块的目的：wails 是 CGO 依赖，不让它污染根模块的纯静态（CGO_ENABLED=0）
// 服务器构建与 CI 门禁。
replace github.com/Autumn-27/artex => ../
