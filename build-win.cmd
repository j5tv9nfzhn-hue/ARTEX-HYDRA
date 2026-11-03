@echo off
setlocal EnableExtensions
cd /d "%~dp0"
set VERSION=2.5.0.7-winfix
set LOG=%~dp0dist\build-win.log
if not exist "%~dp0dist" mkdir "%~dp0dist"
echo ==== BUILD START %DATE% %TIME% ==== > "%LOG%"

echo [1/6] frontend static export >> "%LOG%" 2>&1
cd /d "%~dp0web"
set NEXT_EXPORT=1
set NEXT_PUBLIC_APP_VERSION=%VERSION%
call npx next build >> "%LOG%" 2>&1
if errorlevel 1 (echo FE_BUILD_FAILED >> "%LOG%" & exit /b 1)
cd /d "%~dp0"

echo [2/6] mirror web\out -^> server\webui\dist >> "%LOG%" 2>&1
robocopy "%~dp0web\out" "%~dp0server\webui\dist" /MIR /NFL /NDL /NJH /NJS /NP >> "%LOG%" 2>&1
if errorlevel 8 (echo MIRROR_FAILED >> "%LOG%" & exit /b 1)

echo [3/6] go build windows/amd64 >> "%LOG%" 2>&1
if not exist "%~dp0dist\artex-rea-windows-amd64" mkdir "%~dp0dist\artex-rea-windows-amd64"
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
set GOSUMDB=sum.golang.org
go build -tags embedui -trimpath -ldflags "-s -w -buildid= -X main.version=%VERSION%" -o "%~dp0dist\artex-rea-windows-amd64\artex-rea.exe" ./cmd/artex >> "%LOG%" 2>&1
if errorlevel 1 (echo GO_BUILD_FAILED >> "%LOG%" & exit /b 1)

echo [4/6] stage package dir >> "%LOG%" 2>&1
set PKG=%~dp0dist\artex-rea-%VERSION%-windows-amd64
if exist "%PKG%" rmdir /s /q "%PKG%"
mkdir "%PKG%"
copy /y "%~dp0dist\artex-rea-windows-amd64\artex-rea.exe" "%PKG%\artex-rea.exe" >> "%LOG%" 2>&1
copy /y "%~dp0dist\artex-rea-windows-amd64\artex-rea.exe" "%PKG%\artex-rea-core.exe" >> "%LOG%" 2>&1
copy /y "%~dp0start.bat" "%PKG%\" >> "%LOG%" 2>&1
copy /y "%~dp0start-lan.bat" "%PKG%\" >> "%LOG%" 2>&1
copy /y "%~dp0config.example.json" "%PKG%\" >> "%LOG%" 2>&1
if exist "%~dp0README.md" copy /y "%~dp0README.md" "%PKG%\" >> "%LOG%" 2>&1
xcopy "%~dp0skills" "%PKG%\skills" /E /I /Q /Y >> "%LOG%" 2>&1
if exist "%PKG%\skills\embed.go" del /q "%PKG%\skills\embed.go"

echo [5/6] zip via bsdtar >> "%LOG%" 2>&1
if exist "%PKG%.zip" del /q "%PKG%.zip"
C:\Windows\System32\tar.exe -a -c -f "%PKG%.zip" -C "%~dp0dist" "artex-rea-%VERSION%-windows-amd64" >> "%LOG%" 2>&1
if errorlevel 1 (echo ZIP_FAILED >> "%LOG%" & exit /b 1)

echo [6/6] checksum >> "%LOG%" 2>&1
cd /d "%~dp0dist"
certutil -hashfile "artex-rea-%VERSION%-windows-amd64.zip" SHA256 > "artex-rea-%VERSION%-windows-amd64.zip.sha256" 2>> "%LOG%"
echo ==== BUILD DONE %DATE% %TIME% ==== >> "%LOG%"
exit /b 0
