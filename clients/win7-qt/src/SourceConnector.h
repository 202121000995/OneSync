#pragma once

#include "Endpoint.h"
#include "SyncLink.h"
#include "SyncProtocol.h"

#include <QAtomicInt>
#include <QMap>
#include <QObject>
#include <QSslSocket>
#include <QStringList>

class SourceConnector : public QObject
{
    Q_OBJECT

public:
    SourceConnector(const SyncLink& link, const QString& sourceFolder, const QStringList& ignoreRules);

public slots:
    void run();
    void cancel();

signals:
    void logMessage(const QString& message);
    void statusChanged(const QString& status);
    void trafficChanged(quint64 receivedBytes, quint64 sentBytes);
    void snapshotScanned(quint64 fileCount, quint64 byteCount, quint64 ignoredCount);
    void planReceived(int operationCount, quint64 standardBytes);
    void finished(bool ok, const QString& message);

private:
    struct SnapshotEntry {
        QString path;
        qint64 size = 0;
        QString hash;
    };

    bool isCancelled(QString* error) const;
    bool connectTls(QSslSocket* socket, const Endpoint& endpoint, int timeoutMs, QString* error);
    bool registerRelay(QSslSocket* socket, const QByteArray& token, QString* error);
    bool authenticateTarget(QSslSocket* socket, const QByteArray& token, QString* error);
    bool runSourceSync(QSslSocket* socket, QString* error);
    bool decodeSnapshot(const QByteArray& json, QMap<QString, SnapshotEntry>* files, QString* error) const;
    QList<SnapshotEntry> compareSnapshots(const QMap<QString, SnapshotEntry>& source, const QMap<QString, SnapshotEntry>& target) const;
    bool sendFile(QSslSocket* socket, quint64 requestID, const SnapshotEntry& entry, QString* error);
    bool writeAll(QSslSocket* socket, const QByteArray& data, int timeoutMs, QString* error);
    QByteArray readExact(QSslSocket* socket, int size, int timeoutMs, QString* error);
    bool readFrame(QSslSocket* socket, int timeoutMs, SyncProtocol::Frame* frame, QString* error);
    bool expectAck(QSslSocket* socket, quint64 requestID, int timeoutMs, QByteArray* payload, QString* error);
    QByteArray fileHash(const QString& absolutePath, QString* error) const;
    QByteArray makeFileID(const QString& relativePath, const QByteArray& hash) const;
    QByteArray encodeFileBegin(const QString& relativePath, qint64 size, const QByteArray& hash, const QByteArray& fileID) const;
    QByteArray encodeFileChunk(qint64 offset, const QByteArray& data) const;
    QByteArray encodeFileEnd(qint64 size, const QByteArray& hash) const;
    qint64 decodeOffset(const QByteArray& payload, QString* error) const;
    void emitTrafficIfChanged();

    SyncLink link;
    QString sourceFolder;
    QStringList ignoreRules;
    QAtomicInt cancelled = 0;
    quint64 receivedBytes = 0;
    quint64 sentBytes = 0;
    quint64 lastReportedReceivedBytes = 0;
    quint64 lastReportedSentBytes = 0;
};
