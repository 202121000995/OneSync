#include "SyncLink.h"

#include <QJsonDocument>
#include <QJsonObject>

namespace {
const int kLinkVersion = 1;
const int kMaxEncodedLink = 16 * 1024;
const int kTokenLength = 32;

QDateTime parseDateTime(const QJsonValue& value)
{
    const QString text = value.toString();
    QDateTime parsed = QDateTime::fromString(text, Qt::ISODateWithMs);
    if (!parsed.isValid()) {
        parsed = QDateTime::fromString(text, Qt::ISODate);
    }
    return parsed.toUTC();
}
} // namespace

bool SyncLink::hasRelay() const
{
    return !relayEndpoint.trimmed().isEmpty();
}

QString SyncLink::sourceCertificatePem() const
{
    return sourceCaCertificatePem.trimmed().isEmpty() ? caCertificatePem : sourceCaCertificatePem;
}

QString SyncLink::relayCertificatePem() const
{
    return relayCaCertificatePem.trimmed().isEmpty() ? caCertificatePem : relayCaCertificatePem;
}

QByteArray SyncLink::decodedToken() const
{
    return QByteArray::fromBase64(
        token.toUtf8(),
        QByteArray::Base64UrlEncoding | QByteArray::OmitTrailingEquals
    );
}

bool SyncLinkParser::parse(const QString& encoded, SyncLink* link, QString* error)
{
    return parseWithExpiryMode(encoded, true, link, error);
}

bool SyncLinkParser::parseStored(const QString& encoded, SyncLink* link, QString* error)
{
    return parseWithExpiryMode(encoded, false, link, error);
}

bool SyncLinkParser::parseWithExpiryMode(const QString& encoded, bool rejectExpired, SyncLink* link, QString* error)
{
    if (link == nullptr) {
        if (error != nullptr) {
            *error = QStringLiteral("内部错误：链接结果为空。");
        }
        return false;
    }
    const QByteArray decoded = decodeBase64Url(encoded.trimmed(), error);
    if (decoded.isEmpty()) {
        return false;
    }

    QJsonParseError parseError;
    const QJsonDocument document = QJsonDocument::fromJson(decoded, &parseError);
    if (parseError.error != QJsonParseError::NoError || !document.isObject()) {
        if (error != nullptr) {
            *error = QStringLiteral("同步链接格式不正确。");
        }
        return false;
    }

    const QJsonObject object = document.object();
    SyncLink parsed;
    parsed.version = object.value(QStringLiteral("version")).toInt();
    parsed.sessionId = object.value(QStringLiteral("session_id")).toString();
    parsed.endpoint = object.value(QStringLiteral("endpoint")).toString();
    parsed.relayEndpoint = object.value(QStringLiteral("relay_endpoint")).toString();
    parsed.relayToken = object.value(QStringLiteral("relay_token")).toString();
    parsed.caCertificatePem = object.value(QStringLiteral("ca_certificate_pem")).toString();
    parsed.sourceCaCertificatePem = object.value(QStringLiteral("source_ca_certificate_pem")).toString();
    parsed.relayCaCertificatePem = object.value(QStringLiteral("relay_ca_certificate_pem")).toString();
    parsed.token = object.value(QStringLiteral("token")).toString();
    parsed.issuedAt = parseDateTime(object.value(QStringLiteral("issued_at")));
    parsed.expiresAt = parseDateTime(object.value(QStringLiteral("expires_at")));

    if (!validate(parsed, rejectExpired, error)) {
        return false;
    }
    *link = parsed;
    return true;
}

QByteArray SyncLinkParser::decodeBase64Url(const QString& encoded, QString* error)
{
    if (encoded.isEmpty() || encoded.size() > kMaxEncodedLink) {
        if (error != nullptr) {
            *error = QStringLiteral("同步链接长度不正确。");
        }
        return {};
    }
    const QByteArray input = encoded.toUtf8();
    const QByteArray decoded = QByteArray::fromBase64(
        input,
        QByteArray::Base64UrlEncoding | QByteArray::OmitTrailingEquals
    );
    if (decoded.isEmpty()) {
        if (error != nullptr) {
            *error = QStringLiteral("同步链接不是有效的 Base64URL。");
        }
        return {};
    }
    return decoded;
}

bool SyncLinkParser::validate(const SyncLink& link, bool rejectExpired, QString* error)
{
    if (link.version != kLinkVersion) {
        if (error != nullptr) {
            *error = QStringLiteral("同步链接版本不支持：当前 Win7 客户端只支持链接版本 %1，收到版本 %2。请升级客户端，或让源端重新生成兼容链接。")
                .arg(kLinkVersion)
                .arg(link.version);
        }
        return false;
    }
    if (link.sessionId.trimmed().isEmpty() || link.sessionId.size() > 128 ||
        link.sessionId.contains(QLatin1Char('/')) ||
        link.sessionId.contains(QLatin1Char('\\')) ||
        link.sessionId.contains(QChar(0))) {
        if (error != nullptr) {
            *error = QStringLiteral("同步链接会话编号不正确。");
        }
        return false;
    }
    if (link.endpoint.trimmed().isEmpty() || link.endpoint.size() > 2048) {
        if (error != nullptr) {
            *error = QStringLiteral("同步链接缺少源端地址。");
        }
        return false;
    }
    if (!link.relayEndpoint.isEmpty() && link.relayEndpoint.size() > 2048) {
        if (error != nullptr) {
            *error = QStringLiteral("Relay 地址过长。");
        }
        return false;
    }
    if (link.relayEndpoint.isEmpty() && !link.relayToken.isEmpty()) {
        if (error != nullptr) {
            *error = QStringLiteral("填写 Relay 令牌时必须同时有 Relay 地址。");
        }
        return false;
    }
    if (link.relayToken.size() > 512) {
        if (error != nullptr) {
            *error = QStringLiteral("Relay 令牌过长。");
        }
        return false;
    }
    const QByteArray tokenBytes = link.decodedToken();
    if (tokenBytes.size() != kTokenLength) {
        if (error != nullptr) {
            *error = QStringLiteral("同步令牌不正确。");
        }
        return false;
    }
    if (!link.issuedAt.isValid() || !link.expiresAt.isValid() || link.expiresAt <= link.issuedAt) {
        if (error != nullptr) {
            *error = QStringLiteral("同步链接时间不正确。");
        }
        return false;
    }
    if (rejectExpired && QDateTime::currentDateTimeUtc() >= link.expiresAt) {
        if (error != nullptr) {
            *error = QStringLiteral("同步链接已过期。");
        }
        return false;
    }
    return true;
}
