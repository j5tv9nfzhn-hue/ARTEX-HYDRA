; ARTEX-HYDRA 桌面端安装引导程序（NSIS）。
;
; 基于 wails v2.17.0 的 project.nsi 模板改造，差异：
;   - 简体中文界面 + user 作用域（%LOCALAPPDATA%\Programs，免 UAC——与
;     就地自更新架构契合：Program Files 会让无提权的应用内更新写不进去）；
;   - 旧版 WEB 端（artex-rea）数据导入页：自动探测 + 手动选择，安装段经
;     nsExec 调用 artex-hydra.exe -import-old 完成实际拷贝（Go 侧负责
;     校验与清单输出，见 desktop/importold.go）；
;   - 图标来自仓库品牌资产（web/public/logo.png 生成的多层 ico）。
;
; 构建入口在 build.sh 的 package_desktop（windows 目标、makensis 可用时），
; 传入 define：PRODUCT_VERSION / SOURCE_DIR（打包根目录）/
; ICON_FILE（ico 路径）/ OUT_FILE（setup exe 输出路径）。

Unicode true
ManifestDPIAware true

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "nsDialogs.nsh"
!include "FileFunc.nsh"

; ---- 构建期 define（build.sh 传入）----
!ifndef PRODUCT_VERSION
  !define PRODUCT_VERSION "dev"
!endif
!ifndef SOURCE_DIR
  !error "缺少 SOURCE_DIR define：请经 build.sh 的 package_desktop 调用 makensis"
!endif
!ifndef ICON_FILE
  !define ICON_FILE "artex-hydra.ico"
!endif
!ifndef OUT_FILE
  !error "缺少 OUT_FILE define：请经 build.sh 的 package_desktop 调用 makensis"
!endif

; ---- 常量 ----
!define PRODUCT_NAME "ARTEX-HYDRA"
!define PRODUCT_UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\ARTEX-HYDRA"

Name "${PRODUCT_NAME}"
Caption "${PRODUCT_NAME} ${PRODUCT_VERSION} 安装向导"
OutFile "${OUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\${PRODUCT_NAME}"
InstallDirRegKey HKCU "${PRODUCT_UNINST_KEY}" "InstallLocation"
RequestExecutionLevel user
ShowInstDetails show
ShowUnInstDetails show

!define MUI_ICON "${ICON_FILE}"
!define MUI_UNICON "${ICON_FILE}"
!define MUI_ABORTWARNING

!define MUI_WELCOMEPAGE_TITLE "${PRODUCT_NAME} 安装向导"
!define MUI_WELCOMEPAGE_TEXT "本向导将把 ${PRODUCT_NAME} 桌面端安装到本机。$\r$\n$\r$\n安装过程中可以选择自定义安装目录，并可从旧版 WEB 端（artex-rea）安装目录导入既有数据：配置、数据库（任务/发现/资产/LLM 配置）、技能与工作空间文件。"

!define MUI_FINISHPAGE_RUN "$INSTDIR\artex-hydra.exe"
!define MUI_FINISHPAGE_RUN_TEXT "启动 ${PRODUCT_NAME}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
Page custom ImportPageCreate ImportPageLeave
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

; ---- 导入页状态 ----
Var ImportCheck      ; 复选框控件句柄
Var OldDirField      ; 目录输入框控件句柄
Var OldDirSelected   ; 离开导入页时确定的旧目录（空 = 不导入）

; ---- 导入页：创建 ----
Function ImportPageCreate
  !insertmacro MUI_HEADER_TEXT "导入旧版 WEB 端数据" "可选步骤：从旧版 artex-rea 安装目录导入配置、数据库、技能与工作空间文件"

  nsDialogs::Create 1018
  Pop $0

  ${NSD_CreateLabel} 0 0 100% 32u "可从旧版 WEB 端（artex-rea）安装目录导入：config.json、artex.db（任务/发现/资产/LLM 配置）、credential.key、技能与 data/ 工作空间。$\n导入是纯拷贝——旧目录不会被修改，随时可以切回旧版。"
  Pop $0

  ${NSD_CreateCheckbox} 0 40u 100% 10u "从旧版 WEB 端目录导入数据（需先完全退出旧版程序）"
  Pop $ImportCheck
  ${NSD_SetState} $ImportCheck ${BST_CHECKED}

  ${NSD_CreateLabel} 0 56u 100% 8u "旧版 WEB 端安装目录："
  Pop $0

  ${NSD_CreateText} 0 66u 78% 12u ""
  Pop $OldDirField

  ${NSD_CreateButton} 80% 65u 20% 14u "浏览..."
  Pop $0
  ${NSD_OnClick} $0 OnBrowseOldDir

  Call DetectOldInstall

  nsDialogs::Show
FunctionEnd

; OnBrowseOldDir 弹出系统目录选择框。
Function OnBrowseOldDir
  Pop $0 ; NSD_OnClick 回调会把控件句柄压栈，取走避免栈失衡
  ${NSD_GetText} $OldDirField $1
  nsDialogs::SelectFolderDialog "选择旧版 WEB 端安装目录" "$1"
  Pop $0
  ${If} $0 != "error"
    ${NSD_SetText} $OldDirField $0
  ${EndIf}
FunctionEnd

; DetectOldInstall 自动探测：桌面下含 artex-rea.exe 的目录（旧版默认解压位置）。
Function DetectOldInstall
  FindFirst $R0 $R1 "$DESKTOP\*.*"
detect_loop:
  StrCmp $R1 "" detect_done
  IfFileExists "$DESKTOP\$R1\artex-rea.exe" 0 detect_next
    ${NSD_SetText} $OldDirField "$DESKTOP\$R1"
    FindClose $R0
    Goto detect_done
detect_next:
  FindNext $R0 $R1
  Goto detect_loop
detect_done:
  FindClose $R0
FunctionEnd

; ---- 导入页：离开校验 ----
Function ImportPageLeave
  ${NSD_GetState} $ImportCheck $0
  ${If} $0 == ${BST_CHECKED}
    ${NSD_GetText} $OldDirField $OldDirSelected
    ${If} $OldDirSelected == ""
      MessageBox MB_ICONEXCLAMATION "已勾选导入但未填写旧版目录：请填写、点浏览选择，或取消勾选。"
      Abort
    ${EndIf}
    ${IfNot} ${FileExists} "$OldDirSelected\artex.db"
      MessageBox MB_ICONEXCLAMATION "所选目录中找不到 artex.db：请确认这是旧版 WEB 端的安装目录。"
      Abort
    ${EndIf}
  ${Else}
    StrCpy $OldDirSelected ""
  ${EndIf}
FunctionEnd

; ---- 安装段 ----
Section "Install"
  SetOutPath "$INSTDIR"

  File "${SOURCE_DIR}\artex-hydra.exe"
  File "${SOURCE_DIR}\artex-hydra-core.exe"
  File "${SOURCE_DIR}\config.example.json"
  File "${SOURCE_DIR}\artex-hydra.ico"
  File /oname=README.md "${SOURCE_DIR}\README.md"

  ; 技能包：相对结构随通配符落进 $INSTDIR\skills
  SetOutPath "$INSTDIR\skills"
  File /r "${SOURCE_DIR}\skills\*.*"

  ; 回到根目录：后续快捷方式与导入都相对它
  SetOutPath "$INSTDIR"

  ; 快捷方式（图标取自 exe 内嵌资源）
  CreateDirectory "$SMPROGRAMS\${PRODUCT_NAME}"
  CreateShortCut "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk" "$INSTDIR\artex-hydra.exe" "" "$INSTDIR\artex-hydra.exe" 0
  CreateShortCut "$DESKTOP\${PRODUCT_NAME}.lnk" "$INSTDIR\artex-hydra.exe" "" "$INSTDIR\artex-hydra.exe" 0

  ; 「应用与功能」卸载登记（HKCU：user 作用域无需提权）
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "Publisher" "${PRODUCT_NAME}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "DisplayIcon" "$INSTDIR\artex-hydra.exe"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${PRODUCT_UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${PRODUCT_UNINST_KEY}" "NoRepair" 1
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; 旧版数据导入：实际拷贝/校验逻辑在 Go 侧（-import-old），
  ; 输出经 nsExec 进安装日志面板；退出码非 0 时提示但不算安装失败。
  ${If} $OldDirSelected != ""
    DetailPrint "正在从旧版 WEB 端导入数据（旧目录全程只读）..."
    nsExec::ExecToLog '"$INSTDIR\artex-hydra.exe" -import-old "$OldDirSelected"'
    Pop $R0
    ${If} $R0 != 0
      MessageBox MB_ICONEXCLAMATION "旧版数据导入未完成（退出码 $R0）。$\r$\n$\r$\n程序已安装并可正常使用全新数据。$\r$\n常见原因：旧版程序仍在运行（请完全退出后重试）。$\r$\n也可稍后手动执行：$\r$\n  artex-hydra.exe -import-old \"旧版目录\""
    ${Else}
      DetailPrint "旧版数据导入完成。"
    ${EndIf}
  ${EndIf}
SectionEnd

; ---- 完成后由 MUI_FINISHPAGE_RUN 启动应用 ----

; ---- 卸载器：程序文件与快捷方式删除，用户数据（artex.db/data/密钥/技能）保留 ----
Section Uninstall
  Delete "$INSTDIR\artex-hydra.exe"
  Delete "$INSTDIR\artex-hydra-core.exe"
  Delete "$INSTDIR\config.example.json"
  Delete "$INSTDIR\artex-hydra.ico"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\uninstall.exe"
  RMDir /r "$INSTDIR\skills"
  RMDir "$INSTDIR"

  Delete "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk"
  RMDir "$SMPROGRAMS\${PRODUCT_NAME}"
  Delete "$DESKTOP\${PRODUCT_NAME}.lnk"

  DeleteRegKey HKCU "${PRODUCT_UNINST_KEY}"
  ; 卸载完成提示：数据保留位置
  MessageBox MB_ICONINFORMATION "已卸载 ${PRODUCT_NAME}。$\r$\n用户数据（artex.db、data、技能、密钥）已保留在：$\r$\n$INSTDIR$\r$\n如不再需要可手动删除该目录。"
SectionEnd
