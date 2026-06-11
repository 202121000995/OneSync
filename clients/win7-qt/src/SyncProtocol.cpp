#include "SyncProtocol.h"

#include <QtEndian>

namespace {
const quint8 kRelayRegistrationVersion = 2;
const quint8 kRelayControlJoinVersion = 3;
const quint8 kRelaySessionJoinVersion = 4;
const quint8 kProtocolVersion = 1;
const quint8 kIdentityVersion = 1;
const int kFrameHeaderSize = 14;
const int kTokenSize = 32;
const int kSessionKeySize = 32;
const int kMaxSessionIDLength = 128;
const int kMaxAccessTokenLength = 512;
const int kMaxControlPayloadLength = 1024;

QByteArray buildRelayJoin(quint8 version, const QString& sessionID, quint8 role, const QByteArray& token, const QString& accessToken)
{
    const QByteArray session = sessionID.toUtf8();
    const QByteArray access = accessToken.toUtf8();
    if (session.isEmpty() || session.size() > kMaxSessionIDLength || token.size() != kTokenSize || access.size() > kMaxAccessTokenLength) {
        return {};
    }

    QByteArray data;
    data.resize(6);
    data[0] = char(version);
    data[1] = char(role);
    qToBigEndian<quint16>(quint16(session.size()), reinterpret_cast<uchar*>(data.data() + 2));
    qToBigEndian<quint16>(quint16(access.size()), reinterpret_cast<uchar*>(data.data() + 4));
    data.append(session);
    data.append(token);
    data.append(access);
    return data;
}
} // namespace

QByteArray SyncProtocol::buildRelayRegistration(const QString& sessionID, quint8 role, const QByteArray& token, const QString& accessToken)
{
    return buildRelayJoin(kRelayRegistrationVersion, sessionID, role, token, accessToken);
}

QByteArray SyncProtocol::buildRelayControlJoin(const QString& sessionID, quint8 role, const QByteArray& token, const QString& accessToken)
{
    return buildRelayJoin(kRelayControlJoinVersion, sessionID, role, token, accessToken);
}

QByteArray SyncProtocol::buildRelaySessionJoin(const QString& sessionID, quint8 role, const QByteArray& sessionKey)
{
    const QByteArray session = sessionID.toUtf8();
    if (session.isEmpty() || session.size() > kMaxSessionIDLength || sessionKey.size() != kSessionKeySize) {
        return {};
    }

    QByteArray data;
    data.resize(4);
    data[0] = char(kRelaySessionJoinVersion);
    data[1] = char(role);
    qToBigEndian<quint16>(quint16(session.size()), reinterpret_cast<uchar*>(data.data() + 2));
    data.append(session);
    data.append(sessionKey);
    return data;
}

QByteArray SyncProtocol::buildRelayControlMessage(quint8 type, const QByteArray& payload)
{
    if (type < RelayControlRequestSession || type > RelayControlWake || payload.size() > kMaxControlPayloadLength) {
        return {};
    }
    QByteArray data;
    data.resize(3);
    data[0] = char(type);
    qToBigEndian<quint16>(quint16(payload.size()), reinterpret_cast<uchar*>(data.data() + 1));
    data.append(payload);
    return data;
}

QByteArray SyncProtocol::buildPeerAuthenticationFrame(quint64 requestID, const QString& peerID, const QByteArray& token)
{
    const QByteArray peer = peerID.toUtf8();
    if (peer.isEmpty() || peer.size() > 128 || token.size() < kTokenSize) {
        return {};
    }

    QByteArray payload;
    payload.resize(3);
    payload[0] = char(kIdentityVersion);
    qToBigEndian<quint16>(quint16(peer.size()), reinterpret_cast<uchar*>(payload.data() + 1));
    payload.append(peer);
    payload.append(token);
    return buildFrame(MessageAuthenticate, requestID, payload);
}

QByteArray SyncProtocol::buildFrame(quint8 type, quint64 requestID, const QByteArray& payload)
{
    QByteArray data;
    data.resize(kFrameHeaderSize);
    data[0] = char(kProtocolVersion);
    data[1] = char(type);
    qToBigEndian<quint64>(requestID, reinterpret_cast<uchar*>(data.data() + 2));
    qToBigEndian<quint32>(quint32(payload.size()), reinterpret_cast<uchar*>(data.data() + 10));
    data.append(payload);
    return data;
}

bool SyncProtocol::parseFrame(const QByteArray& header, const QByteArray& payload, Frame* frame, QString* error)
{
    if (frame == nullptr) {
        if (error != nullptr) {
            *error = QStringLiteral("内部错误：帧结果为空。");
        }
        return false;
    }
    if (header.size() != kFrameHeaderSize) {
        if (error != nullptr) {
            *error = QStringLiteral("网络帧头长度不正确。");
        }
        return false;
    }
    if (quint8(header[0]) != kProtocolVersion) {
        if (error != nullptr) {
            *error = QStringLiteral("网络协议版本不支持。");
        }
        return false;
    }
    const quint32 payloadLength = qFromBigEndian<quint32>(reinterpret_cast<const uchar*>(header.constData() + 10));
    if (payloadLength != quint32(payload.size())) {
        if (error != nullptr) {
            *error = QStringLiteral("网络帧负载长度不一致。");
        }
        return false;
    }
    frame->type = quint8(header[1]);
    frame->requestID = qFromBigEndian<quint64>(reinterpret_cast<const uchar*>(header.constData() + 2));
    frame->payload = payload;
    return true;
}
