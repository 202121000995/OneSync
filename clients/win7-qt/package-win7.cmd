@echo off
setlocal enabledelayedexpansion

set "ROOT=%~dp0"
cd /d "%ROOT%"

set "APP_NAME=OneSyncWin7"
set "VERSION=1.08"
set "BUILD_DIR=%ROOT%build-win7"
set "DIST_ROOT=%ROOT%dist"
set "DIST_NAME=%APP_NAME%-win7-qt-v%VERSION%"
set "DIST_DIR=%DIST_ROOT%\%DIST_NAME%"
set "ZIP_PATH=%DIST_ROOT%\%DIST_NAME%.zip"

if "%QMAKE%"=="" set "QMAKE=qmake"
if "%MAKE%"=="" set "MAKE=nmake"

call :resolve_command "%QMAKE%" QMAKE_EXE
if errorlevel 1 (
    echo Cannot find qmake.
    echo Please run this script from a Qt 5 command prompt, or set QMAKE=C:\Qt\5.12.12\msvc2017\bin\qmake.exe
    exit /b 1
)

call :resolve_command "%MAKE%" MAKE_EXE
if errorlevel 1 (
    call :resolve_command "mingw32-make" MAKE_EXE
    if errorlevel 1 (
        echo Cannot find nmake or mingw32-make.
        echo Please run this script from a Visual Studio Qt command prompt, or set MAKE=mingw32-make.
        exit /b 1
    )
)

if exist "%BUILD_DIR%" rmdir /s /q "%BUILD_DIR%"
if exist "%DIST_DIR%" rmdir /s /q "%DIST_DIR%"
if exist "%ZIP_PATH%" del /q "%ZIP_PATH%"
mkdir "%BUILD_DIR%"
mkdir "%DIST_DIR%"

pushd "%BUILD_DIR%"
"%QMAKE_EXE%" "%ROOT%OneSyncWin7.pro" "CONFIG+=release"
if errorlevel 1 exit /b 1

"%MAKE_EXE%"
if errorlevel 1 exit /b 1
popd

set "BUILT_EXE=%BUILD_DIR%\release\%APP_NAME%.exe"
if not exist "%BUILT_EXE%" set "BUILT_EXE=%BUILD_DIR%\%APP_NAME%.exe"
if not exist "%BUILT_EXE%" (
    echo Build finished, but %APP_NAME%.exe was not found.
    exit /b 1
)

copy "%BUILT_EXE%" "%DIST_DIR%\%APP_NAME%.exe" >nul
copy "%ROOT%README.md" "%DIST_DIR%\README.md" >nul
mkdir "%DIST_DIR%\docs" >nul 2>nul
copy "%ROOT%docs\protocol-notes.md" "%DIST_DIR%\docs\protocol-notes.md" >nul

for %%Q in ("%QMAKE_EXE%") do set "QT_BIN=%%~dpQ"
set "WINDEPLOYQT=%QT_BIN%windeployqt.exe"
if exist "%WINDEPLOYQT%" (
    "%WINDEPLOYQT%" --release --no-translations "%DIST_DIR%\%APP_NAME%.exe"
) else (
    echo windeployqt.exe not found. Qt DLLs were not copied automatically.
)

set "OPENSSL_COPIED=0"
for %%F in ("%QT_BIN%libssl*.dll" "%QT_BIN%libcrypto*.dll" "%QT_BIN%ssleay32.dll" "%QT_BIN%libeay32.dll") do (
    if exist "%%~F" (
        copy "%%~F" "%DIST_DIR%\" >nul
        set "OPENSSL_COPIED=1"
    )
)
for %%D in (libssl-1_1-x64.dll libcrypto-1_1-x64.dll libssl-1_1.dll libcrypto-1_1.dll ssleay32.dll libeay32.dll) do (
    if not exist "%DIST_DIR%\%%D" call :copy_from_path "%%D"
)
if "%OPENSSL_COPIED%"=="0" (
    echo.
    echo Warning: OpenSSL DLLs were not found automatically.
    echo If TLS fails on Win7, copy libssl/libcrypto DLLs matching this Qt build into:
    echo %DIST_DIR%
)

call :write_build_info
call :create_zip

echo.
echo Win7 Qt package written to:
echo %DIST_DIR%
echo.
echo Main executable:
echo %DIST_DIR%\%APP_NAME%.exe
if exist "%ZIP_PATH%" (
    echo.
    echo Zip package:
    echo %ZIP_PATH%
)

endlocal
exit /b 0

:resolve_command
set "CANDIDATE=%~1"
set "%~2="
if exist "%CANDIDATE%" (
    set "%~2=%CANDIDATE%"
    exit /b 0
)
for /f "delims=" %%P in ('where "%CANDIDATE%" 2^>nul') do (
    set "%~2=%%P"
    exit /b 0
)
exit /b 1

:copy_from_path
for /f "delims=" %%P in ('where "%~1" 2^>nul') do (
    copy "%%P" "%DIST_DIR%\" >nul
    set "OPENSSL_COPIED=1"
    exit /b 0
)
exit /b 0

:write_build_info
(
    echo OneSync Win7 Qt package
    echo Version: %VERSION%
    echo Built at: %DATE% %TIME%
    echo qmake: %QMAKE_EXE%
    echo make: %MAKE_EXE%
) > "%DIST_DIR%\BUILD.txt"
exit /b 0

:create_zip
where powershell >nul 2>nul
if errorlevel 1 (
    echo PowerShell not found. Zip package was not created.
    exit /b 0
)
powershell -NoProfile -ExecutionPolicy Bypass -Command "if (Get-Command Compress-Archive -ErrorAction SilentlyContinue) { Compress-Archive -Path '%DIST_DIR%\*' -DestinationPath '%ZIP_PATH%' -Force; exit 0 } exit 2"
if errorlevel 1 (
    echo Compress-Archive is unavailable. Zip package was not created.
)
exit /b 0
