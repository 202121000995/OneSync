#pragma once

#include <QByteArray>
#include <QString>
#include <QtGlobal>

namespace SyncProtocol {

const quint8 RelayRoleTarget = 2;
const quint8 RelayControlRequestSession = 1;
const quint8 RelayControlInviteSession = 2;
const quint8 RelayControlError = 3;
const quint8 RelayControlPing = 4;
const quint8 RelayControlPong = 5;
const quint8 RelayControlWake = 6;
const quint8 MessageAuthenticate = 2;
const quint8 MessageSnapshotRequest = 3;
const quint8 MessageSnapshotResponse = 4;
const quint8 MessageSyncPlan = 5;
const quint8 MessageFileBegin = 6;
const quint8 MessageFileChunk = 7;
const quint8 MessageFileEnd = 8;
const quint8 MessageSyncComplete = 9;
const quint8 MessageAck = 10;
const quint8 MessageError = 11;

struct Frame
{
    quint8 type = 0;
    quint64 requestID = 0;
    QByteArray payload;
};

QByteArray buildRelayRegistration(const QString& sessionID, quint8 role, const QByteArray& token, const QString& accessToken);
QByteArray buildRelayControlJoin(const QString& sessionID, quint8 role, const QByteArray& token, const QString& accessToken);
QByteArray buildRelaySessionJoin(const QString& sessionID, quint8 role, const QByteArray& sessionKey);
QByteArray buildRelayControlMessage(quint8 type, const QByteArray& payload);
QByteArray buildPeerAuthenticationFrame(quint64 requestID, const QString& peerID, const QByteArray& token);
QByteArray buildFrame(quint8 type, quint64 requestID, const QByteArray& payload);
bool parseFrame(const QByteArray& header, const QByteArray& payload, Frame* frame, QString* error);

} // namespace SyncProtocol
