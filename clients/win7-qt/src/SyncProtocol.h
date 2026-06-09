#pragma once

#include <QByteArray>
#include <QString>
#include <QtGlobal>

namespace SyncProtocol {

const quint8 RelayRoleTarget = 2;
const quint8 MessageAuthenticate = 2;
const quint8 MessageAck = 10;
const quint8 MessageError = 11;

struct Frame
{
    quint8 type = 0;
    quint64 requestID = 0;
    QByteArray payload;
};

QByteArray buildRelayRegistration(const QString& sessionID, quint8 role, const QByteArray& token, const QString& accessToken);
QByteArray buildPeerAuthenticationFrame(quint64 requestID, const QString& peerID, const QByteArray& token);
QByteArray buildFrame(quint8 type, quint64 requestID, const QByteArray& payload);
bool parseFrame(const QByteArray& header, const QByteArray& payload, Frame* frame, QString* error);

} // namespace SyncProtocol
