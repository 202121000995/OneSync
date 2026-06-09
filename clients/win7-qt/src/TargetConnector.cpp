#include "TargetConnector.h"

#include "FileReceiver.h"
#include "PeerIdentity.h"
#include "SnapshotScanner.h"

#include <QElapsedTimer>
#include <QFileInfo>
#include <QJsonDocument>
#include <QJsonObject>
#include <QSslCertificate>
#include <QtEndian>

namespace {
const int kTlsTimeoutMs = 15000;
const int kRelayWaitTimeoutMs = 30000;
const int kAuthenticationTimeoutMs = 15000;
const int kSyncMessageTimeoutMs = 30000;
const int kMaxPayload = 16 * 1024 * 1024;
const quint64 kAuthenticationRequestID = 1;

QString shortPeerID(const QString& peerID)
{
    if (peerID.size() <= 12) {
        return peerID;
    }
    return peerID.left(8) + QStringLiteral("...");
}
} // namespace

TargetConnector::TargetConnector(const SyncLink& link, const QString& targetFolder)
    : link(link)
    , targetFolder(targetFolder)
{
}

void TargetConnector::run()
{
    emit statusChanged(QStringLiteral("运行-连接中"));

    const QFileInfo targetInfo(targetFolder);
    if (!targetInfo.exists() || !targetInfo.isDir()) {
        emit finished(false, QStringLiteral("接收文件夹不存在或不是目录。"));
        return;
    }

    const QByteArray token = link.decodedToken();
    if (token.size() != 32) {
        emit finished(false, QStringLiteral("同步令牌不正确。"));
        return;
    }

    const QString peerID = PeerIdentityStore::peerIDForSession(link.sessionId);
    emit logMessage(QStringLiteral("本机目标端身份：%1").arg(shortPeerID(peerID)));

    Endpoint endpoint;
    QString error;
    const QString endpointText = link.hasRelay() ? link.relayEndpoint : link.endpoint;
    if (!EndpointParser::parse(endpointText, &endpoint, &error)) {
        emit finished(false, QStringLiteral("连接地址错误：%1").arg(error));
        return;
    }

    QSslSocket socket;
    if (!connectTls(&socket, endpoint, kTlsTimeoutMs, &error)) {
        emit finished(false, error);
        return;
    }

    if (link.hasRelay()) {
        emit logMessage(QStringLiteral("已连接 Relay TLS：%1").arg(endpoint.display()));
        emit logMessage(QStringLiteral("等待 Relay 配对源端。"));
        if (!registerRelay(&socket, token, &error)) {
            emit finished(false, error);
            return;
        }
        emit logMessage(QStringLiteral("Relay 已配对，开始同步认证。"));
    } else {
        emit logMessage(QStringLiteral("已直连源端 TLS：%1").arg(endpoint.display()));
    }

    if (!authenticate(&socket, token, peerID, &error)) {
        emit finished(false, error);
        return;
    }

    emit statusChanged(QStringLiteral("运行-已连接源端"));
    if (!respondSnapshot(&socket, &error)) {
        emit finished(false, error);
        return;
    }
    if (!receivePlan(&socket, &error)) {
        emit finished(false, error);
        return;
    }
    emit finished(true, QStringLiteral("本轮同步完成。"));
}

bool TargetConnector::connectTls(QSslSocket* socket, const Endpoint& endpoint, int timeoutMs, QString* error)
{
    const QList<QSslCertificate> certificates = QSslCertificate::fromData(link.caCertificatePem.toUtf8(), QSsl::Pem);
    if (!certificates.isEmpty()) {
        socket->setCaCertificates(certificates);
    }
    socket->setPeerVerifyMode(QSslSocket::VerifyPeer);
    socket->setPeerVerifyName(endpoint.host);

    emit logMessage(QStringLiteral("正在建立 TLS 连接：%1").arg(endpoint.display()));
    socket->connectToHostEncrypted(endpoint.host, endpoint.port);
    if (!socket->waitForEncrypted(timeoutMs)) {
        const QString detail = socket->errorString();
        if (error != nullptr) {
            *error = QStringLiteral("TLS 连接失败：%1").arg(detail);
        }
        return false;
    }
    return true;
}

bool TargetConnector::respondSnapshot(QSslSocket* socket, QString* error)
{
    emit logMessage(QStringLiteral("等待源端请求目标端快照。"));
    SyncProtocol::Frame request;
    if (!readFrame(socket, kSyncMessageTimeoutMs, &request, error)) {
        return false;
    }
    if (request.type != SyncProtocol::MessageSnapshotRequest) {
        if (error != nullptr) {
            *error = QStringLiteral("源端没有按预期请求目标端快照。");
        }
        return false;
    }

    emit logMessage(QStringLiteral("开始扫描目标端目录。"));
    QByteArray snapshotJson;
    quint64 fileCount = 0;
    quint64 byteCount = 0;
    if (!SnapshotScanner::scanToJson(targetFolder, &snapshotJson, &fileCount, &byteCount, error)) {
        return false;
    }

    emit logMessage(QStringLiteral("目标端快照完成：%1 个文件，%2 字节。").arg(fileCount).arg(byteCount));
    const QByteArray response = SyncProtocol::buildFrame(
        SyncProtocol::MessageSnapshotResponse,
        request.requestID,
        snapshotJson
    );
    if (!writeAll(socket, response, kSyncMessageTimeoutMs, error)) {
        return false;
    }
    emit logMessage(QStringLiteral("已发送目标端快照，等待源端同步计划。"));
    return true;
}

bool TargetConnector::receivePlan(QSslSocket* socket, QString* error)
{
    SyncProtocol::Frame planFrame;
    if (!readFrame(socket, kSyncMessageTimeoutMs, &planFrame, error)) {
        return false;
    }
    if (planFrame.type != SyncProtocol::MessageSyncPlan) {
        if (error != nullptr) {
            *error = QStringLiteral("源端没有按预期发送同步计划。");
        }
        return false;
    }

    QJsonParseError parseError;
    const QJsonDocument document = QJsonDocument::fromJson(planFrame.payload, &parseError);
    if (parseError.error != QJsonParseError::NoError || !document.isObject()) {
        if (error != nullptr) {
            *error = QStringLiteral("同步计划格式不正确。");
        }
        return false;
    }
    const QJsonObject object = document.object();
    const int operationCount = object.value(QStringLiteral("operation_count")).toInt(-1);
    const double standardBytes = object.value(QStringLiteral("standard_bytes")).toDouble(0);
    emit logMessage(QStringLiteral("收到同步计划：%1 个文件，标准大小约 %2 字节。").arg(operationCount).arg(qulonglong(standardBytes)));
    if (operationCount < 0) {
        if (error != nullptr) {
            *error = QStringLiteral("同步计划文件数量不正确。");
        }
        return false;
    }

    const QByteArray ack = SyncProtocol::buildFrame(SyncProtocol::MessageAck, planFrame.requestID, QByteArray());
    if (!writeAll(socket, ack, kSyncMessageTimeoutMs, error)) {
        return false;
    }
    emit logMessage(QStringLiteral("同步计划已确认。"));

    for (int index = 0; index < operationCount; ++index) {
        SyncProtocol::Frame begin;
        if (!readFrame(socket, kSyncMessageTimeoutMs, &begin, error)) {
            return false;
        }
        QString transferredPath;
        emit logMessage(QStringLiteral("开始接收文件：%1 / %2").arg(index + 1).arg(operationCount));
        if (!FileReceiver::receive(socket, targetFolder, begin, &transferredPath, error)) {
            return false;
        }
        emit logMessage(QStringLiteral("文件接收完成：%1").arg(transferredPath));
    }

    SyncProtocol::Frame complete;
    if (!readFrame(socket, kSyncMessageTimeoutMs, &complete, error)) {
        return false;
    }
    if (complete.type != SyncProtocol::MessageSyncComplete) {
        if (error != nullptr) {
            *error = QStringLiteral("源端没有按预期发送同步完成消息。");
        }
        return false;
    }
    const QByteArray completeAck = SyncProtocol::buildFrame(SyncProtocol::MessageAck, complete.requestID, QByteArray());
    if (!writeAll(socket, completeAck, kSyncMessageTimeoutMs, error)) {
        return false;
    }
    emit logMessage(QStringLiteral("本轮同步已完成。"));
    return true;
}

bool TargetConnector::registerRelay(QSslSocket* socket, const QByteArray& token, QString* error)
{
    const QByteArray registration = SyncProtocol::buildRelayRegistration(
        link.sessionId,
        SyncProtocol::RelayRoleTarget,
        token,
        link.relayToken
    );
    if (registration.isEmpty()) {
        if (error != nullptr) {
            *error = QStringLiteral("Relay 登记数据生成失败。");
        }
        return false;
    }
    if (!writeAll(socket, registration, kAuthenticationTimeoutMs, error)) {
        return false;
    }
    const QByteArray ready = readExact(socket, 1, kRelayWaitTimeoutMs, error);
    if (ready.size() != 1) {
        return false;
    }
    if (quint8(ready[0]) != 1) {
        if (error != nullptr) {
            *error = QStringLiteral("Relay 返回了无效的配对响应。");
        }
        return false;
    }
    return true;
}

bool TargetConnector::authenticate(QSslSocket* socket, const QByteArray& token, const QString& peerID, QString* error)
{
    const QByteArray frame = SyncProtocol::buildPeerAuthenticationFrame(kAuthenticationRequestID, peerID, token);
    if (frame.isEmpty()) {
        if (error != nullptr) {
            *error = QStringLiteral("同步认证数据生成失败。");
        }
        return false;
    }
    emit logMessage(QStringLiteral("正在向源端发送同步认证。"));
    if (!writeAll(socket, frame, kAuthenticationTimeoutMs, error)) {
        return false;
    }

    SyncProtocol::Frame response;
    if (!readFrame(socket, kAuthenticationTimeoutMs, &response, error)) {
        return false;
    }
    if (response.requestID != kAuthenticationRequestID || response.type != SyncProtocol::MessageAck) {
        if (error != nullptr) {
            *error = response.type == SyncProtocol::MessageError
                ? QStringLiteral("源端拒绝同步认证。")
                : QStringLiteral("源端认证响应不正确。");
        }
        return false;
    }
    return true;
}

bool TargetConnector::writeAll(QSslSocket* socket, const QByteArray& data, int timeoutMs, QString* error)
{
    qint64 offset = 0;
    while (offset < data.size()) {
        const qint64 written = socket->write(data.constData() + offset, data.size() - offset);
        if (written < 0) {
            if (error != nullptr) {
                *error = QStringLiteral("网络写入失败：%1").arg(socket->errorString());
            }
            return false;
        }
        offset += written;
        if (!socket->waitForBytesWritten(timeoutMs)) {
            if (error != nullptr) {
                *error = QStringLiteral("网络写入超时：%1").arg(socket->errorString());
            }
            return false;
        }
    }
    return true;
}

QByteArray TargetConnector::readExact(QSslSocket* socket, int size, int timeoutMs, QString* error)
{
    QByteArray data;
    QElapsedTimer timer;
    timer.start();
    while (data.size() < size) {
        if (socket->bytesAvailable() <= 0) {
            const int remaining = timeoutMs - int(timer.elapsed());
            if (remaining <= 0 || !socket->waitForReadyRead(remaining)) {
                if (error != nullptr) {
                    *error = QStringLiteral("网络读取超时或失败：%1").arg(socket->errorString());
                }
                return {};
            }
        }
        data.append(socket->read(size - data.size()));
    }
    return data;
}

bool TargetConnector::readFrame(QSslSocket* socket, int timeoutMs, SyncProtocol::Frame* frame, QString* error)
{
    const QByteArray header = readExact(socket, 14, timeoutMs, error);
    if (header.size() != 14) {
        return false;
    }
    const quint32 payloadLength = qFromBigEndian<quint32>(reinterpret_cast<const uchar*>(header.constData() + 10));
    if (payloadLength > kMaxPayload) {
        if (error != nullptr) {
            *error = QStringLiteral("源端响应过大。");
        }
        return false;
    }
    const QByteArray payload = payloadLength == 0 ? QByteArray() : readExact(socket, int(payloadLength), timeoutMs, error);
    if (payload.size() != int(payloadLength)) {
        return false;
    }
    return SyncProtocol::parseFrame(header, payload, frame, error);
}
