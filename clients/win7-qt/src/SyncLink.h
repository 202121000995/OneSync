#pragma once

#include <QByteArray>
#include <QDateTime>
#include <QString>

struct SyncLink
{
    int version = 0;
    QString sessionId;
    QString endpoint;
    QString relayEndpoint;
    QString relayToken;
    QString caCertificatePem;
    QString token;
    QDateTime issuedAt;
    QDateTime expiresAt;

    bool hasRelay() const;
    QByteArray decodedToken() const;
};

class SyncLinkParser
{
public:
    static bool parse(const QString& encoded, SyncLink* link, QString* error);

private:
    static QByteArray decodeBase64Url(const QString& encoded, QString* error);
    static bool validate(const SyncLink& link, QString* error);
};
