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
    QString sourceCaCertificatePem;
    QString relayCaCertificatePem;
    QString token;
    QDateTime issuedAt;
    QDateTime expiresAt;

    bool hasRelay() const;
    QString sourceCertificatePem() const;
    QString relayCertificatePem() const;
    QByteArray decodedToken() const;
};

class SyncLinkParser
{
public:
    static bool parse(const QString& encoded, SyncLink* link, QString* error);
    static bool parseStored(const QString& encoded, SyncLink* link, QString* error);

private:
    static bool parseWithExpiryMode(const QString& encoded, bool rejectExpired, SyncLink* link, QString* error);
    static QByteArray decodeBase64Url(const QString& encoded, QString* error);
    static bool validate(const SyncLink& link, bool rejectExpired, QString* error);
};
