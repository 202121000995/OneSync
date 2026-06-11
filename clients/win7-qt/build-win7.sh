#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$ROOT/../.." && pwd)
TOOLS_ROOT=${TOOLS_ROOT:-/Users/apple/Documents/轻量化定时备份/tools}
ZIG=${ZIG:-"$TOOLS_ROOT/zig-macos-aarch64-0.11.0/zig"}
QT_WIN=${QT_WIN:-"$TOOLS_ROOT/Qt/5.12.12/mingw73_32"}
QT_HOST=${QT_HOST:-/Users/apple/Qt5.12.12/5.12.12/clang_64}
OPENSSL_WIN=${OPENSSL_WIN:-/Users/apple/Documents/建站和证书申请软件/qt/runtime/win32}
VERSION=${ONESYNC_WIN7_VERSION:-1.16}
WIN_TARGET=${WIN_TARGET:-x86-windows-gnu}

BUILD="$ROOT/.build-win7"
RELEASE="$ROOT/release-win7"
DIST_ROOT="$ROOT/dist"
DIST_NAME="OneSyncWin7-win7-x86-v$VERSION"
DIST_DIR="$DIST_ROOT/$DIST_NAME"
ZIP_PATH="$DIST_ROOT/$DIST_NAME.zip"

for file in "$ZIG" "$QT_HOST/bin/moc" \
    "$QT_WIN/lib/libQt5Core.a" "$QT_WIN/bin/Qt5Core.dll" \
    "$QT_WIN/bin/Qt5Gui.dll" "$QT_WIN/bin/Qt5Widgets.dll" "$QT_WIN/bin/Qt5Network.dll" \
    "$QT_WIN/plugins/platforms/qwindows.dll" \
    "$OPENSSL_WIN/libcrypto-1_1.dll" "$OPENSSL_WIN/libssl-1_1.dll" \
    "$REPO_ROOT/packaging/icons/OneSync.ico" "$ROOT/tools/make_icon_res.py"; do
    if [ ! -e "$file" ]; then
        echo "Missing build dependency: $file" >&2
        exit 1
    fi
done

rm -rf "$BUILD" "$RELEASE" "$DIST_DIR" "$ZIP_PATH"
mkdir -p "$BUILD/moc" "$BUILD/obj" "$BUILD/zig-cache" "$BUILD/zig-global-cache" \
    "$RELEASE/platforms" "$DIST_ROOT"

DEFINES="-DUNICODE -D_UNICODE -DWIN32 -D_WIN32 -D_WIN32_WINNT=0x0601 -DWINVER=0x0601 -DNOMINMAX \
-DQT_NO_DEBUG -DQT_WIDGETS_LIB -DQT_GUI_LIB -DQT_NETWORK_LIB -DQT_CORE_LIB"
INCLUDES="-I$ROOT -I$ROOT/src -I$BUILD/moc -I$QT_WIN/include \
-I$QT_WIN/include/QtCore -I$QT_WIN/include/QtGui \
-I$QT_WIN/include/QtWidgets -I$QT_WIN/include/QtNetwork"

"$QT_HOST/bin/moc" $DEFINES $INCLUDES "$ROOT/src/MainWindow.h" -o "$BUILD/moc/moc_MainWindow.cpp"
"$QT_HOST/bin/moc" $DEFINES $INCLUDES "$ROOT/src/SourceConnector.h" -o "$BUILD/moc/moc_SourceConnector.cpp"
"$QT_HOST/bin/moc" $DEFINES $INCLUDES "$ROOT/src/TargetConnector.h" -o "$BUILD/moc/moc_TargetConnector.cpp"

export ZIG_LOCAL_CACHE_DIR="$BUILD/zig-cache"
export ZIG_GLOBAL_CACHE_DIR="$BUILD/zig-global-cache"

compile()
{
    source_file=$1
    object_file=$2
    "$ZIG" c++ -target "$WIN_TARGET" -std=c++17 -O2 \
        -fno-exceptions -fno-rtti -Wno-ignored-attributes $DEFINES $INCLUDES \
        -c "$source_file" -o "$object_file"
}

compile "$ROOT/src/main.cpp" "$BUILD/obj/main.obj"
compile "$ROOT/src/Endpoint.cpp" "$BUILD/obj/Endpoint.obj"
compile "$ROOT/src/FileReceiver.cpp" "$BUILD/obj/FileReceiver.obj"
compile "$ROOT/src/IgnoreMatcher.cpp" "$BUILD/obj/IgnoreMatcher.obj"
compile "$ROOT/src/MainWindow.cpp" "$BUILD/obj/MainWindow.obj"
compile "$ROOT/src/PeerIdentity.cpp" "$BUILD/obj/PeerIdentity.obj"
compile "$ROOT/src/SourceConnector.cpp" "$BUILD/obj/SourceConnector.obj"
compile "$ROOT/src/SnapshotScanner.cpp" "$BUILD/obj/SnapshotScanner.obj"
compile "$ROOT/src/SyncLink.cpp" "$BUILD/obj/SyncLink.obj"
compile "$ROOT/src/SyncProtocol.cpp" "$BUILD/obj/SyncProtocol.obj"
compile "$ROOT/src/TargetConnector.cpp" "$BUILD/obj/TargetConnector.obj"
compile "$BUILD/moc/moc_MainWindow.cpp" "$BUILD/obj/moc_MainWindow.obj"
compile "$BUILD/moc/moc_SourceConnector.cpp" "$BUILD/obj/moc_SourceConnector.obj"
compile "$BUILD/moc/moc_TargetConnector.cpp" "$BUILD/obj/moc_TargetConnector.obj"

python3 "$ROOT/tools/make_icon_res.py" "$REPO_ROOT/packaging/icons/OneSync.ico" "$BUILD/obj/OneSync.res"

"$ZIG" c++ -target "$WIN_TARGET" -O2 -nostdlib++ \
    "$BUILD/obj/main.obj" \
    "$BUILD/obj/Endpoint.obj" \
    "$BUILD/obj/FileReceiver.obj" \
    "$BUILD/obj/IgnoreMatcher.obj" \
    "$BUILD/obj/MainWindow.obj" \
    "$BUILD/obj/PeerIdentity.obj" \
    "$BUILD/obj/SourceConnector.obj" \
    "$BUILD/obj/SnapshotScanner.obj" \
    "$BUILD/obj/SyncLink.obj" \
    "$BUILD/obj/SyncProtocol.obj" \
    "$BUILD/obj/TargetConnector.obj" \
    "$BUILD/obj/moc_MainWindow.obj" \
    "$BUILD/obj/moc_SourceConnector.obj" \
    "$BUILD/obj/moc_TargetConnector.obj" \
    "$BUILD/obj/OneSync.res" \
    -L"$QT_WIN/lib" \
    -L"$QT_WIN/bin" \
    -lqtmain -lQt5Widgets -lQt5Gui -lQt5Network -lQt5Core \
    "$QT_WIN/bin/libstdc++-6.dll" \
    -Wl,--subsystem=windows \
    -o "$RELEASE/OneSyncWin7.exe"

for dll in Qt5Core.dll Qt5Gui.dll Qt5Widgets.dll Qt5Network.dll \
    libgcc_s_dw2-1.dll libstdc++-6.dll libwinpthread-1.dll; do
    cp "$QT_WIN/bin/$dll" "$RELEASE/$dll"
done

cp "$QT_WIN/plugins/platforms/qwindows.dll" "$RELEASE/platforms/qwindows.dll"
cp "$OPENSSL_WIN/libcrypto-1_1.dll" "$RELEASE/libcrypto-1_1.dll"
cp "$OPENSSL_WIN/libssl-1_1.dll" "$RELEASE/libssl-1_1.dll"
cp "$ROOT/README.md" "$RELEASE/README.md"
mkdir -p "$RELEASE/docs"
cp "$ROOT/docs/protocol-notes.md" "$RELEASE/docs/protocol-notes.md"
rm -f "$RELEASE"/*.pdb "$RELEASE"/*.lib

{
    echo "OneSync Win7 Qt package"
    echo "Version: $VERSION"
    echo "Target: Windows 7 x86"
    echo "Zig target: $WIN_TARGET"
    echo "Qt: 5.12.12 mingw73_32"
    echo "Built at: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
} > "$RELEASE/BUILD.txt"

cp -R "$RELEASE" "$DIST_DIR"
(
    cd "$DIST_ROOT"
    zip -qry "$ZIP_PATH" "$DIST_NAME"
)

echo "Windows 7 x86 package created:"
echo "$DIST_DIR"
echo "$ZIP_PATH"
