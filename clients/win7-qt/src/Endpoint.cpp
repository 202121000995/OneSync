#include "Endpoint.h"

bool Endpoint::isValid() const
{
    return !host.trimmed().isEmpty() && port > 0;
}

QString Endpoint::display() const
{
    if (host.contains(QLatin1Char(':')) && !host.startsWith(QLatin1Char('['))) {
        return QStringLiteral("[%1]:%2").arg(host).arg(port);
    }
    return QStringLiteral("%1:%2").arg(host).arg(port);
}

bool EndpointParser::parse(const QString& value, Endpoint* endpoint, QString* error)
{
    if (endpoint == nullptr) {
        if (error != nullptr) {
            *error = QStringLiteral("内部错误：地址结果为空。");
        }
        return false;
    }
    const QString text = value.trimmed();
    if (text.isEmpty()) {
        if (error != nullptr) {
            *error = QStringLiteral("地址为空。");
        }
        return false;
    }

    QString host;
    QString portText;
    if (text.startsWith(QLatin1Char('['))) {
        const int end = text.indexOf(QLatin1Char(']'));
        if (end < 0 || end + 2 > text.size() || text.at(end + 1) != QLatin1Char(':')) {
            if (error != nullptr) {
                *error = QStringLiteral("IPv6 地址格式不正确。");
            }
            return false;
        }
        host = text.mid(1, end - 1);
        portText = text.mid(end + 2);
    } else {
        const int colon = text.lastIndexOf(QLatin1Char(':'));
        if (colon <= 0 || colon == text.size() - 1) {
            if (error != nullptr) {
                *error = QStringLiteral("地址必须包含端口，例如 192.168.1.36:7443。");
            }
            return false;
        }
        host = text.left(colon);
        portText = text.mid(colon + 1);
    }

    bool ok = false;
    const uint port = portText.toUInt(&ok);
    if (host.trimmed().isEmpty() || !ok || port == 0 || port > 65535) {
        if (error != nullptr) {
            *error = QStringLiteral("地址端口不正确。");
        }
        return false;
    }

    endpoint->host = host;
    endpoint->port = static_cast<quint16>(port);
    return true;
}
