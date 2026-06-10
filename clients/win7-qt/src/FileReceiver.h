#pragma once

#include "SyncProtocol.h"

#include <QByteArray>
#include <QAtomicInt>
#include <QString>
#include <QStringList>

#include <functional>

class QSslSocket;

class FileReceiver
{
public:
    struct Options {
        QStringList ignoreRules;
        QAtomicInt* cancelled = nullptr;
        quint64* receivedBytes = nullptr;
        quint64* sentBytes = nullptr;
        std::function<void()> onTrafficChanged;
        bool skipped = false;
    };

    static bool receive(QSslSocket* socket, const QString& root, const SyncProtocol::Frame& beginFrame, Options* options, QString* transferredPath, QString* error);

private:
    static bool decodeBegin(const QByteArray& payload, QString* path, qint64* size, QByteArray* hash, QByteArray* fileID, QString* error);
    static bool decodeChunk(const QByteArray& payload, qint64* offset, QByteArray* data, QString* error);
    static bool decodeEnd(const QByteArray& payload, qint64* size, QByteArray* hash, QString* error);
    static bool validateRelativePath(const QString& path, QString* error);
    static QString safeTargetPath(const QString& root, const QString& relativePath, QString* error);
    static bool prepareTargetParent(const QString& root, const QString& relativePath, QString* error);
    static bool preparePartDir(const QString& root, QString* partDir, QString* error);
    static QByteArray makeFileID(const QString& path, const QByteArray& hash);
    static QByteArray encodeOffset(qint64 offset);
    static QByteArray hashFile(const QString& path, QString* error);
    static bool isCancelled(const Options* options, QString* error);
    static bool discardFile(QSslSocket* socket, const SyncProtocol::Frame& beginFrame, qint64 totalSize, const QByteArray& expectedHash, Options* options, QString* error);
    static bool writeAck(QSslSocket* socket, quint64 requestID, const QByteArray& payload, Options* options, QString* error);
    static bool writeError(QSslSocket* socket, quint64 requestID, Options* options);
    static bool writeAll(QSslSocket* socket, const QByteArray& data, int timeoutMs, Options* options, QString* error);
    static QByteArray readExact(QSslSocket* socket, int size, int timeoutMs, Options* options, QString* error);
    static bool readFrame(QSslSocket* socket, int timeoutMs, SyncProtocol::Frame* frame, Options* options, QString* error);
};
