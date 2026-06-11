#pragma once

#include "Endpoint.h"
#include "SyncLink.h"
#include "SyncProtocol.h"

#include <QAtomicInt>
#include <QObject>
#include <QSslSocket>
#include <QStringList>

class TargetConnector : public QObject
{
    Q_OBJECT

public:
    TargetConnector(const SyncLink& link, const QString& targetFolder, const QStringList& ignoreRules);

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
    bool isCancelled(QString* error) const;
    bool waitBeforeRetry(QString* error) const;
    bool waitBeforeConnectedCycle(QString* error) const;
    bool connectTls(QSslSocket* socket, const Endpoint& endpoint, int timeoutMs, QString* error);
    bool registerRelay(QSslSocket* socket, const QByteArray& token, QString* error);
    bool authenticate(QSslSocket* socket, const QByteArray& token, const QString& peerID, QString* error);
    bool respondSnapshot(QSslSocket* socket, QString* error);
    bool receivePlan(QSslSocket* socket, QString* error);
    bool writeAll(QSslSocket* socket, const QByteArray& data, int timeoutMs, QString* error);
    QByteArray readExact(QSslSocket* socket, int size, int timeoutMs, QString* error);
    bool readFrame(QSslSocket* socket, int timeoutMs, SyncProtocol::Frame* frame, QString* error);
    void emitTrafficIfChanged();

    SyncLink link;
    QString targetFolder;
    QStringList ignoreRules;
    QAtomicInt cancelled = 0;
    quint64 receivedBytes = 0;
    quint64 sentBytes = 0;
    quint64 lastReportedReceivedBytes = 0;
    quint64 lastReportedSentBytes = 0;
};
