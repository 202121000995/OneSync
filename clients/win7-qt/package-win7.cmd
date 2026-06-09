@echo off
setlocal enabledelayedexpansion

set "ROOT=%~dp0"
cd /d "%ROOT%"

set "APP_NAME=OneSyncWin7"
set "VERSION=0.1.0"
set "BUILD_DIR=%ROOT%build-win7"
set "DIST_DIR=%ROOT%dist\%APP_NAME%-win7-qt-v%VERSION%"

if "%QMAKE%"=="" set "QMAKE=qmake"
if "%MAKE%"=="" set "MAKE=nmake"

where "%QMAKE%" >nul 2>nul
if errorlevel 1 (
    echo Cannot find qmake.
    echo Please run this script from a Qt 5 command prompt, or set QMAKE=C:\Qt\5.12.12\msvc2017\bin\qmake.exe
    exit /b 1
)

where "%MAKE%" >nul 2>nul
if errorlevel 1 (
    where mingw32-make >nul 2>nul
    if errorlevel 1 (
        echo Cannot find nmake or mingw32-make.
        echo Please run this script from a Visual Studio Qt command prompt, or set MAKE=mingw32-make.
        exit /b 1
    )
    set "MAKE=mingw32-make"
)

if exist "%BUILD_DIR%" rmdir /s /q "%BUILD_DIR%"
if exist "%DIST_DIR%" rmdir /s /q "%DIST_DIR%"
mkdir "%BUILD_DIR%"
mkdir "%DIST_DIR%"

pushd "%BUILD_DIR%"
"%QMAKE%" "%ROOT%OneSyncWin7.pro" "CONFIG+=release"
if errorlevel 1 exit /b 1

"%MAKE%"
if errorlevel 1 exit /b 1
popd

set "BUILT_EXE=%BUILD_DIR%\release\%APP_NAME%.exe"
if not exist "%BUILT_EXE%" set "BUILT_EXE=%BUILD_DIR%\%APP_NAME%.exe"
if not exist "%BUILT_EXE%" (
    echo Build finished, but %APP_NAME%.exe was not found.
    exit /b 1
)

copy "%BUILT_EXE%" "%DIST_DIR%\%APP_NAME%.exe" >nul

for %%Q in ("%QMAKE%") do set "QT_BIN=%%~dpQ"
set "WINDEPLOYQT=%QT_BIN%windeployqt.exe"
if exist "%WINDEPLOYQT%" (
    "%WINDEPLOYQT%" --release --no-translations "%DIST_DIR%\%APP_NAME%.exe"
) else (
    echo windeployqt.exe not found. Qt DLLs were not copied automatically.
)

echo.
echo Win7 Qt package written to:
echo %DIST_DIR%
echo.
echo Main executable:
echo %DIST_DIR%\%APP_NAME%.exe

endlocal
