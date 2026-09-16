@echo off
REM ====================================================================
REM  muse-cc build script (Windows / cmd)
REM  Builds the React panel and embeds it into a single Go binary.
REM ====================================================================
setlocal enabledelayedexpansion
cd /d "%~dp0"

echo [1/4] Building web panel...
pushd web
call npm install --include=dev --no-audit --no-fund
if errorlevel 1 ( echo npm install failed & popd & exit /b 1 )
call node_modules\.bin\vite.cmd build
if errorlevel 1 ( echo vite build failed & popd & exit /b 1 )
popd

echo [2/4] Copying dist into embed folder...
REM Keep the embed folder and its tracked .gitkeep, but clear built assets.
if not exist "internal\assets\dist" mkdir "internal\assets\dist"
for %%F in ("internal\assets\dist\*") do if /i not "%%~nxF"==".gitkeep" del /q "%%F" 2>nul
for /d %%D in ("internal\assets\dist\*") do rmdir /s /q "%%D" 2>nul
xcopy web\dist\* internal\assets\dist\ /e /i /y /q | findstr /v "File(s)" >nul

echo [3/4] Compiling Go binary...
set LDFLAGS=-s -w
go build -ldflags "%LDFLAGS%" -o muse-cc.exe .
if errorlevel 1 ( echo go build failed & exit /b 1 )

echo [4/4] Done.
echo.
echo   Binary:  muse-cc.exe
echo   Run it:  muse-cc.exe           (then open http://localhost:8788)
echo   Test:    go test ./...
echo.
endlocal
