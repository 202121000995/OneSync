#pragma once

#include "Endpoint.h"
#include "SyncLink.h"
#include "SyncProtocol.h"

#include <QObject>
#include <QSslSocket>

class TargetConnector : public QObject
{
    Q_OBJECT

public:
    TargetConnector(const SyncLink& link, const QString& targetFolder);

public slots:
    void run();

signals:
    void logMessage(const QString& message);
    void statusChanged(const QString& status);
    void finished(bool ok, const QString& message);

private:
    bool connectTls(QSslSocket* socket, const Endpoint& endpoint, int timeoutMs, QString* error);
    bool registerRelay(QSslSocket* socket, const QByteArray& token, QString* error);
    bool authenticate(QSslSocket* socket, const QByteArray& token, const QString& peerID, QString* error);
    bool writeAll(QSslSocket* socket, const QByteArray& data, int timeoutMs, QString* error);
    QByteArray readExact(QSslSocket* socket, int size, int timeoutMs, QString* error);
    bool readFrame(QSslSocket* socket, int timeoutMs, SyncProtocol::Frame* frame, QString* error);

    SyncLink link;
    QString targetFolder;
};
