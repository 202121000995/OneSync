#pragma once

#include "SyncProtocol.h"

#include <QByteArray>
#include <QString>

class QSslSocket;

class FileReceiver
{
public:
    static bool receive(QSslSocket* socket, const QString& root, const SyncProtocol::Frame& beginFrame, QString* transferredPath, QString* error);

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
    static bool writeAck(QSslSocket* socket, quint64 requestID, const QByteArray& payload, QString* error);
    static bool writeError(QSslSocket* socket, quint64 requestID);
    static bool writeAll(QSslSocket* socket, const QByteArray& data, int timeoutMs, QString* error);
    static QByteArray readExact(QSslSocket* socket, int size, int timeoutMs, QString* error);
    static bool readFrame(QSslSocket* socket, int timeoutMs, SyncProtocol::Frame* frame, QString* error);
};
