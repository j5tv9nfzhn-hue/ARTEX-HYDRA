@echo off
setlocal EnableExtensions
cd /d "%~dp0"

rem 本地 Windows 单平台打包脚本（CI 走 build.sh --release，不用这个）。
rem
rem 与发行形态保持一致：只有一个 artex-hydra.exe，不再附带 core 副本，
rem 也不再附带 start.bat / start-lan.bat（入口程序自带监护重启，
rem 且那两个脚本已在 bab04288 里删除）。旧版这里还叫 artex-rea.exe
rem 并复制一份 artex-rea-core.exe，改完名后一直没同步，跑起来会：
rem   1) 产出旧名的 EXE 与目录；
rem   2) copy start.bat 因文件不存在而失败，整包缺启动脚本。
set VERSION=dev
if not "%~1"=="" set VERSION=%~1
set PKGNAME=artex-hydra-%VERSION%-windows-amd64
set STAGE=%~dp0dist\%PKGNAME%
set LOG=%~dp0dist\build-win.log
if not exist "%~dp0dist" mkdir "%~dp0dist"
echo ==== BUILD START %DATE% %TIME% ==== > "%LOG%"

echo [1/5] frontend static export >> "%LOG%" 2>&1
cd /d "%~dp0web"
set NEXT_EXPORT=1
set NEXT_PUBLIC_APP_VERSION=%VERSION%
call npx next build >> "%LOG%" 2>&1
if errorlevel 1 (echo FE_BUILD_FAILED >> "%LOG%" & exit /b 1)
cd /d "%~dp0"

echo [2/5] mirror web\out -^> server\webui\dist >> "%LOG%" 2>&1
robocopy "%~dp0web\out" "%~dp0server\webui\dist" /MIR /NFL /NDL /NJH /NJS /NP >> "%LOG%" 2>&1
if errorlevel 8 (echo MIRROR_FAILED >> "%LOG%" & exit /b 1)

echo [3/5] go build windows/amd64 >> "%LOG%" 2>&1
if exist "%STAGE%" rmdir /s /q "%STAGE%"
mkdir "%STAGE%"
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
set GOSUMDB=sum.golang.org
go build -tags embedui -trimpath -ldflags "-s -w -buildid= -X main.version=%VERSION%" -o "%STAGE%\artex-hydra.exe" ./cmd/artex >> "%LOG%" 2>&1
if errorlevel 1 (echo GO_BUILD_FAILED >> "%LOG%" & exit /b 1)

echo [4/5] stage runtime assets >> "%LOG%" 2>&1
copy /y "%~dp0config.example.json" "%STAGE%\" >> "%LOG%" 2>&1
if exist "%~dp0README.md" copy /y "%~dp0README.md" "%STAGE%\" >> "%LOG%" 2>&1
xcopy "%~dp0skills" "%STAGE%\skills" /E /I /Q /Y >> "%LOG%" 2>&1
if exist "%STAGE%\skills\embed.go" del /q "%STAGE%\skills\embed.go"

echo [5/5] zip via bsdtar >> "%LOG%" 2>&1
if exist "%STAGE%.zip" del /q "%STAGE%.zip"
C:\Windows\System32\tar.exe -a -c -f "%STAGE%.zip" -C "%~dp0dist" "%PKGNAME%" >> "%LOG%" 2>&1
if errorlevel 1 (echo ZIP_FAILED >> "%LOG%" & exit /b 1)
cd /d "%~dp0dist"
certutil -hashfile "%PKGNAME%.zip" SHA256 > "%PKGNAME%.zip.sha256" 2>> "%LOG%"
echo ==== BUILD DONE %DATE% %TIME% ==== >> "%LOG%"
exit /b 0
