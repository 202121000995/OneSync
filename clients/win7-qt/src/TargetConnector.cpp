#include "TargetConnector.h"

#include "FileReceiver.h"
#include "PeerIdentity.h"
#include "SnapshotScanner.h"

#include <QCryptographicHash>
#include <QElapsedTimer>
#include <QFileInfo>
#include <QJsonDocument>
#include <QJsonObject>
#include <QThread>
#include <QSslCertificate>
#include <QtEndian>

namespace {
const int kTlsTimeoutMs = 15000;
const int kRelayWaitTimeoutMs = 120000;
const int kRetryDelayMs = 30000;
const int kAuthenticationTimeoutMs = 15000;
const int kSyncMessageTimeoutMs = 120000;
const int kMaxPayload = 16 * 1024 * 1024;
const quint64 kAuthenticationRequestID = 1;

QString shortPeerID(const QString& peerID)
{
    if (peerID.size() <= 12) {
        return peerID;
    }
    return peerID.left(8) + QStringLiteral("...");
}

QByteArray certificateFingerprint(const QSslCertificate& certificate)
{
    return certificate.digest(QCryptographicHash::Sha256).toHex();
}

bool certificateMatchesPinned(const QSslCertificate& certificate, const QList<QSslCertificate>& pinnedCertificates)
{
    if (certificate.isNull()) {
        return false;
    }
    const QByteArray fingerprint = certificateFingerprint(certificate);
    for (const QSslCertificate& pinned : pinnedCertificates) {
        if (certificateFingerprint(pinned) == fingerprint) {
            return true;
        }
    }
    return false;
}
} // namespace

TargetConnector::TargetConnector(const SyncLink& link, const QString& targetFolder, const QStringList& ignoreRules)
    : link(link)
    , targetFolder(targetFolder)
    , ignoreRules(ignoreRules)
{
}

void TargetConnector::cancel()
{
    cancelled.storeRelease(1);
}

void TargetConnector::run()
{
    QString error;
    if (isCancelled(&error)) {
        emit finished(false, error);
        return;
    }

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
    const QByteArray authenticationToken = link.token.toUtf8();
    if (authenticationToken.size() < 32) {
        emit finished(false, QStringLiteral("同步认证令牌不正确。"));
        return;
    }

    const QString peerID = PeerIdentityStore::peerIDForSession(link.sessionId);
    emit logMessage(QStringLiteral("本机目标端身份：%1").arg(shortPeerID(peerID)));

    Endpoint endpoint;
    const QString endpointText = link.hasRelay() ? link.relayEndpoint : link.endpoint;
    if (!EndpointParser::parse(endpointText, &endpoint, &error)) {
        emit finished(false, QStringLiteral("连接地址错误：%1").arg(error));
        return;
    }

    emit logMessage(QStringLiteral("接收端已进入长期等待模式；源端暂未在线时会自动重试。"));
    while (true) {
        if (isCancelled(&error)) {
            emit finished(false, error);
            return;
        }
        emit statusChanged(QStringLiteral("运行-连接中"));
        QString cycleError;
        QSslSocket socket;
        if (!connectTls(&socket, endpoint, kTlsTimeoutMs, &cycleError)) {
            emit logMessage(QStringLiteral("本轮 TLS 连接失败：%1").arg(cycleError));
            if (!waitBeforeRetry(&error)) {
                emit finished(false, error);
                return;
            }
            continue;
        }

        if (link.hasRelay()) {
            emit logMessage(QStringLiteral("已连接 Relay TLS：%1").arg(endpoint.display()));
            emit logMessage(QStringLiteral("等待 Relay 配对源端。"));
            if (!registerRelay(&socket, token, &cycleError)) {
                emit logMessage(QStringLiteral("本轮 Relay 配对未完成：%1").arg(cycleError));
                socket.disconnectFromHost();
                if (!waitBeforeRetry(&error)) {
                    emit finished(false, error);
                    return;
                }
                continue;
            }
            emit logMessage(QStringLiteral("Relay 已配对，开始同步认证。"));
        } else {
            emit logMessage(QStringLiteral("已直连源端 TLS：%1").arg(endpoint.display()));
        }

        if (!authenticate(&socket, authenticationToken, peerID, &cycleError)) {
            emit logMessage(QStringLiteral("本轮同步认证失败：%1").arg(cycleError));
            socket.disconnectFromHost();
            if (!waitBeforeRetry(&error)) {
                emit finished(false, error);
                return;
            }
            continue;
        }

        emit statusChanged(QStringLiteral("运行-已连接源端"));
        if (!respondSnapshot(&socket, &cycleError)) {
            emit logMessage(QStringLiteral("本轮快照响应失败：%1").arg(cycleError));
            socket.disconnectFromHost();
            if (!waitBeforeRetry(&error)) {
                emit finished(false, error);
                return;
            }
            continue;
        }
        if (!receivePlan(&socket, &cycleError)) {
            emit logMessage(QStringLiteral("本轮接收同步计划失败：%1").arg(cycleError));
            socket.disconnectFromHost();
            if (!waitBeforeRetry(&error)) {
                emit finished(false, error);
                return;
            }
            continue;
        }
        emit statusChanged(QStringLiteral("运行-等待"));
        emit logMessage(QStringLiteral("本轮同步完成，继续等待下一轮同步。"));
        socket.disconnectFromHost();
        if (!waitBeforeRetry(&error)) {
            emit finished(false, error);
            return;
        }
    }
}

bool TargetConnector::isCancelled(QString* error) const
{
    if (cancelled.loadAcquire() != 0) {
        if (error != nullptr) {
            *error = QStringLiteral("同步已取消。");
        }
        return true;
    }
    return false;
}

bool TargetConnector::waitBeforeRetry(QString* error) const
{
    for (int elapsed = 0; elapsed < kRetryDelayMs; elapsed += 250) {
        if (isCancelled(error)) {
            return false;
        }
        QThread::msleep(250);
    }
    return true;
}

bool TargetConnector::connectTls(QSslSocket* socket, const Endpoint& endpoint, int timeoutMs, QString* error)
{
    const QList<QSslCertificate> certificates = QSslCertificate::fromData(link.caCertificatePem.toUtf8(), QSsl::Pem);
    if (!certificates.isEmpty()) {
        socket->setCaCertificates(certificates);
        socket->setPeerVerifyMode(QSslSocket::QueryPeer);
    } else {
        socket->setPeerVerifyMode(QSslSocket::VerifyPeer);
    }
    socket->setPeerVerifyName(endpoint.host);

    emit logMessage(QStringLiteral("正在建立 TLS 连接：%1").arg(endpoint.display()));
    socket->connectToHostEncrypted(endpoint.host, endpoint.port);
    if (!socket->waitForEncrypted(timeoutMs)) {
        const QString detail = socket->errorString();
        if (error != nullptr) {
            if (link.hasRelay() && link.caCertificatePem.trimmed().isEmpty() &&
                detail.contains(QStringLiteral("self-signed"), Qt::CaseInsensitive)) {
                *error = QStringLiteral("TLS 连接失败：Relay 使用自签证书，但同步链接没有携带 Relay 证书。请在源端创建同步时填写 Relay 证书，或使用新版 Win7 源端自动写入证书。原始错误：%1").arg(detail);
            } else {
                *error = QStringLiteral("TLS 连接失败：%1").arg(detail);
            }
        }
        return false;
    }
    if (!certificates.isEmpty()) {
        const QSslCertificate peerCertificate = socket->peerCertificate();
        if (!certificateMatchesPinned(peerCertificate, certificates)) {
            if (error != nullptr) {
                *error = QStringLiteral("TLS 连接失败：Relay 返回的证书和同步链接里的 Relay 证书不一致。请让源端重新生成链接，或在 Relay 服务器执行 onesync-relayctl info 后核对证书。");
            }
            return false;
        }
        emit logMessage(QStringLiteral("Relay TLS 证书指纹已匹配。"));
    }
    return true;
}

bool TargetConnector::respondSnapshot(QSslSocket* socket, QString* error)
{
    if (isCancelled(error)) {
        return false;
    }
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
    quint64 ignoredCount = 0;
    if (!SnapshotScanner::scanToJson(targetFolder, ignoreRules, &snapshotJson, &fileCount, &byteCount, &ignoredCount, error)) {
        return false;
    }

    emit snapshotScanned(fileCount, byteCount, ignoredCount);
    emit logMessage(QStringLiteral("目标端快照完成：%1 个文件，%2 字节，忽略 %3 项。").arg(fileCount).arg(byteCount).arg(ignoredCount));
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
    if (isCancelled(error)) {
        return false;
    }
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
    const quint64 standardByteCount = standardBytes > 0 ? quint64(standardBytes) : 0;
    emit planReceived(operationCount, standardByteCount);
    emit logMessage(QStringLiteral("收到同步计划：%1 个文件，标准大小约 %2 字节。").arg(operationCount).arg(standardByteCount));
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
        if (isCancelled(error)) {
            return false;
        }
        SyncProtocol::Frame begin;
        if (!readFrame(socket, kSyncMessageTimeoutMs, &begin, error)) {
            return false;
        }
        QString transferredPath;
        emit logMessage(QStringLiteral("开始接收文件：%1 / %2").arg(index + 1).arg(operationCount));
        FileReceiver::Options options;
        options.ignoreRules = ignoreRules;
        options.cancelled = &cancelled;
        options.receivedBytes = &receivedBytes;
        options.sentBytes = &sentBytes;
        options.onTrafficChanged = [this]() {
            emitTrafficIfChanged();
        };
        if (!FileReceiver::receive(socket, targetFolder, begin, &options, &transferredPath, error)) {
            emitTrafficIfChanged();
            return false;
        }
        emitTrafficIfChanged();
        emit logMessage(options.skipped
            ? QStringLiteral("文件已按忽略规则跳过：%1").arg(transferredPath)
            : QStringLiteral("文件接收完成：%1").arg(transferredPath));
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
        if (error != nullptr) {
            const QString detail = error->trimmed().isEmpty() ? QStringLiteral("Relay 未返回配对确认。") : *error;
            *error = QStringLiteral("Relay 配对失败：源端没有及时启动、目标端重复启动了同一个链接，或 Relay 令牌不匹配。请先启动源端，避免重复点击目标端开始，并查看 Relay 日志。原始错误：%1").arg(detail);
        }
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
    QElapsedTimer idleTimer;
    idleTimer.start();
    while (offset < data.size()) {
        if (isCancelled(error)) {
            return false;
        }
        const qint64 written = socket->write(data.constData() + offset, data.size() - offset);
        if (written < 0) {
            if (error != nullptr) {
                *error = QStringLiteral("网络写入失败：%1").arg(socket->errorString());
            }
            return false;
        }
        if (written > 0) {
            offset += written;
            sentBytes += quint64(written);
            emitTrafficIfChanged();
            idleTimer.restart();
        }
        const int remaining = timeoutMs - int(idleTimer.elapsed());
        if (remaining <= 0) {
            if (error != nullptr) {
                *error = QStringLiteral("网络写入长时间无进展：%1").arg(socket->errorString());
            }
            return false;
        }
        if (!socket->waitForBytesWritten(qMin(remaining, 500)) && idleTimer.elapsed() >= timeoutMs) {
            if (error != nullptr) {
                *error = QStringLiteral("网络写入长时间无进展：%1").arg(socket->errorString());
            }
            return false;
        }
    }
    return true;
}

QByteArray TargetConnector::readExact(QSslSocket* socket, int size, int timeoutMs, QString* error)
{
    QByteArray data;
    QElapsedTimer idleTimer;
    idleTimer.start();
    while (data.size() < size) {
        if (isCancelled(error)) {
            return {};
        }
        if (socket->bytesAvailable() <= 0) {
            const int remaining = timeoutMs - int(idleTimer.elapsed());
            if (remaining <= 0) {
                if (error != nullptr) {
                    *error = QStringLiteral("网络读取长时间无进展或失败：%1").arg(socket->errorString());
                }
                return {};
            }
            if (!socket->waitForReadyRead(qMin(remaining, 500)) && idleTimer.elapsed() >= timeoutMs) {
                if (error != nullptr) {
                    *error = QStringLiteral("网络读取长时间无进展或失败：%1").arg(socket->errorString());
                }
                return {};
            }
        }
        const QByteArray chunk = socket->read(size - data.size());
        if (!chunk.isEmpty()) {
            receivedBytes += quint64(chunk.size());
            emitTrafficIfChanged();
            idleTimer.restart();
        }
        data.append(chunk);
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

void TargetConnector::emitTrafficIfChanged()
{
    if (receivedBytes == lastReportedReceivedBytes && sentBytes == lastReportedSentBytes) {
        return;
    }
    lastReportedReceivedBytes = receivedBytes;
    lastReportedSentBytes = sentBytes;
    emit trafficChanged(receivedBytes, sentBytes);
}
