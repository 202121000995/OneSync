QT += widgets network

CONFIG += c++17
CONFIG -= app_bundle

TARGET = OneSyncWin7
TEMPLATE = app

win32 {
    DEFINES += WINVER=0x0601 _WIN32_WINNT=0x0601 NOMINMAX
    RC_ICONS = ../../packaging/icons/OneSync.ico
}

SOURCES += \
    src/main.cpp \
    src/Endpoint.cpp \
    src/FileReceiver.cpp \
    src/IgnoreMatcher.cpp \
    src/MainWindow.cpp \
    src/PeerIdentity.cpp \
    src/SourceConnector.cpp \
    src/SnapshotScanner.cpp \
    src/SyncLink.cpp \
    src/SyncProtocol.cpp \
    src/TargetConnector.cpp

HEADERS += \
    src/Endpoint.h \
    src/FileReceiver.h \
    src/IgnoreMatcher.h \
    src/MainWindow.h \
    src/PeerIdentity.h \
    src/SourceConnector.h \
    src/SnapshotScanner.h \
    src/SyncLink.h \
    src/SyncProtocol.h \
    src/TargetConnector.h
