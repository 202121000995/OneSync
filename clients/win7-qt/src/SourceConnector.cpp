#include "SourceConnector.h"

#include "SnapshotScanner.h"

#include <QCryptographicHash>
#include <QDir>
#include <QElapsedTimer>
#include <QFile>
#include <QFileInfo>
#include <QJsonDocument>
#include <QJsonObject>
#include <QSslCertificate>
#include <QtEndian>

#include <cstring>
#include <limits>

namespace {
const int kTlsTimeoutMs = 15000;
const int kRelayWaitTimeoutMs = 30000;
const int kAuthenticationTimeoutMs = 15000;
const int kSyncMessageTimeoutMs = 30000;
const int kMaxPayload = 16 * 1024 * 1024;
const int kMaxChunkSize = 256 * 1024;
const int kHashSize = 32;
const quint64 kSnapshotRequestID = 1;
const quint64 kPlanRequestID = 2;
const int kMaxOperations = 1000000;
const quint8 kRelayRoleSource = 1;

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

SourceConnector::SourceConnector(const SyncLink& link, const QString& sourceFolder, const QStringList& ignoreRules)
    : link(link)
    , sourceFolder(sourceFolder)
    , ignoreRules(ignoreRules)
{
}

void SourceConnector::cancel()
{
    cancelled.storeRelease(1);
}

void SourceConnector::run()
{
    emit statusChanged(QStringLiteral("运行-连接中"));
    QString error;
    if (isCancelled(&error)) {
        emit finished(false, error);
        return;
    }

    const QFileInfo sourceInfo(sourceFolder);
    if (!sourceInfo.exists() || !sourceInfo.isDir()) {
        emit finished(false, QStringLiteral("发送文件夹不存在或不是目录。"));
        return;
    }
    if (!link.hasRelay()) {
        emit finished(false, QStringLiteral("Win7 源端当前先支持 Relay 创建同步，请在任务参数里填写 Relay 地址。"));
        return;
    }

    const QByteArray token = link.decodedToken();
    if (token.size() != 32) {
        emit finished(false, QStringLiteral("同步令牌不正确。"));
        return;
    }

    Endpoint endpoint;
    if (!EndpointParser::parse(link.relayEndpoint, &endpoint, &error)) {
        emit finished(false, QStringLiteral("Relay 地址错误：%1").arg(error));
        return;
    }

    QSslSocket socket;
    if (!connectTls(&socket, endpoint, kTlsTimeoutMs, &error)) {
        emit finished(false, error);
        return;
    }

    emit logMessage(QStringLiteral("已连接 Relay TLS：%1").arg(endpoint.display()));
    emit logMessage(QStringLiteral("等待目标端加入同步。"));
    if (!registerRelay(&socket, token, &error)) {
        emit finished(false, error);
        return;
    }
    emit logMessage(QStringLiteral("Relay 已配对，等待目标端认证。"));

    if (!authenticateTarget(&socket, token, &error)) {
        emit finished(false, error);
        return;
    }

    emit statusChanged(QStringLiteral("运行-已连接目标端"));
    if (!runSourceSync(&socket, &error)) {
        emit finished(false, error);
        return;
    }
    emit finished(true, QStringLiteral("本轮同步完成。"));
}

bool SourceConnector::isCancelled(QString* error) const
{
    if (cancelled.loadAcquire() != 0) {
        if (error != nullptr) {
            *error = QStringLiteral("同步已取消。");
        }
        return true;
    }
    return false;
}

bool SourceConnector::connectTls(QSslSocket* socket, const Endpoint& endpoint, int timeoutMs, QString* error)
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
        if (error != nullptr) {
            *error = QStringLiteral("TLS 连接失败：%1").arg(socket->errorString());
        }
        return false;
    }
    if (!certificates.isEmpty()) {
        const QSslCertificate peerCertificate = socket->peerCertificate();
        if (!certificateMatchesPinned(peerCertificate, certificates)) {
            if (error != nullptr) {
                *error = QStringLiteral("TLS 连接失败：Relay 返回的证书和同步链接里的 Relay 证书不一致。请在源端重新生成链接，或在 Relay 服务器执行 onesync-relayctl info 后核对证书。");
            }
            return false;
        }
        emit logMessage(QStringLiteral("Relay TLS 证书指纹已匹配。"));
    }
    return true;
}

bool SourceConnector::registerRelay(QSslSocket* socket, const QByteArray& token, QString* error)
{
    const QByteArray registration = SyncProtocol::buildRelayRegistration(
        link.sessionId,
        kRelayRoleSource,
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

bool SourceConnector::authenticateTarget(QSslSocket* socket, const QByteArray& token, QString* error)
{
    SyncProtocol::Frame request;
    if (!readFrame(socket, kAuthenticationTimeoutMs, &request, error)) {
        return false;
    }
    bool valid = request.type == SyncProtocol::MessageAuthenticate && request.payload.size() >= 3 + token.size() && quint8(request.payload[0]) == 1;
    QString peerID;
    QByteArray receivedToken;
    if (valid) {
        const int peerLength = qFromBigEndian<quint16>(reinterpret_cast<const uchar*>(request.payload.constData() + 1));
        valid = peerLength > 0 && peerLength <= 128 && request.payload.size() == 3 + peerLength + token.size();
        if (valid) {
            peerID = QString::fromUtf8(request.payload.constData() + 3, peerLength);
            receivedToken = request.payload.mid(3 + peerLength);
            const QByteArray left = QCryptographicHash::hash(receivedToken, QCryptographicHash::Sha256);
            const QByteArray right = QCryptographicHash::hash(token, QCryptographicHash::Sha256);
            valid = left == right;
        }
    }

    const quint8 responseType = valid ? SyncProtocol::MessageAck : SyncProtocol::MessageError;
    if (!writeAll(socket, SyncProtocol::buildFrame(responseType, request.requestID, QByteArray()), kAuthenticationTimeoutMs, error)) {
        return false;
    }
    if (!valid) {
        if (error != nullptr) {
            *error = QStringLiteral("目标端同步认证失败。");
        }
        return false;
    }
    emit logMessage(QStringLiteral("目标端认证通过：%1").arg(shortPeerID(peerID)));
    return true;
}

bool SourceConnector::runSourceSync(QSslSocket* socket, QString* error)
{
    if (isCancelled(error)) {
        return false;
    }
    emit logMessage(QStringLiteral("请求目标端快照。"));
    if (!writeAll(socket, SyncProtocol::buildFrame(SyncProtocol::MessageSnapshotRequest, kSnapshotRequestID, QByteArray()), kSyncMessageTimeoutMs, error)) {
        return false;
    }

    SyncProtocol::Frame targetFrame;
    if (!readFrame(socket, kSyncMessageTimeoutMs, &targetFrame, error)) {
        return false;
    }
    if (targetFrame.type != SyncProtocol::MessageSnapshotResponse || targetFrame.requestID != kSnapshotRequestID) {
        if (error != nullptr) {
            *error = QStringLiteral("目标端快照响应不正确。");
        }
        return false;
    }

    QMap<QString, SnapshotEntry> targetFiles;
    if (!decodeSnapshot(targetFrame.payload, &targetFiles, error)) {
        return false;
    }

    emit logMessage(QStringLiteral("开始扫描发送目录。"));
    QByteArray sourceJson;
    quint64 fileCount = 0;
    quint64 byteCount = 0;
    quint64 ignoredCount = 0;
    if (!SnapshotScanner::scanToJson(sourceFolder, ignoreRules, &sourceJson, &fileCount, &byteCount, &ignoredCount, error)) {
        return false;
    }
    emit snapshotScanned(fileCount, byteCount, ignoredCount);
    emit logMessage(QStringLiteral("发送目录快照完成：%1 个文件，%2 字节，忽略 %3 项。").arg(fileCount).arg(byteCount).arg(ignoredCount));

    QMap<QString, SnapshotEntry> sourceFiles;
    if (!decodeSnapshot(sourceJson, &sourceFiles, error)) {
        return false;
    }
    const QList<SnapshotEntry> operations = compareSnapshots(sourceFiles, targetFiles);
    if (operations.size() > kMaxOperations) {
        if (error != nullptr) {
            *error = QStringLiteral("同步计划文件数量过多。");
        }
        return false;
    }

    QByteArray planJson;
    planJson += "{\"operation_count\":";
    planJson += QByteArray::number(operations.size());
    planJson += ",\"standard_files\":";
    planJson += QByteArray::number(fileCount);
    planJson += ",\"standard_bytes\":";
    planJson += QByteArray::number(byteCount);
    planJson += "}";
    if (!writeAll(socket, SyncProtocol::buildFrame(SyncProtocol::MessageSyncPlan, kPlanRequestID, planJson), kSyncMessageTimeoutMs, error)) {
        return false;
    }
    QByteArray ignoredPayload;
    if (!expectAck(socket, kPlanRequestID, kSyncMessageTimeoutMs, &ignoredPayload, error)) {
        return false;
    }
    emit planReceived(operations.size(), byteCount);
    emit logMessage(QStringLiteral("同步计划已发送：%1 个文件。").arg(operations.size()));

    for (int index = 0; index < operations.size(); ++index) {
        if (isCancelled(error)) {
            return false;
        }
        const quint64 requestID = quint64(index) + 3;
        emit logMessage(QStringLiteral("开始发送文件：%1 / %2，%3").arg(index + 1).arg(operations.size()).arg(operations.at(index).path));
        if (!sendFile(socket, requestID, operations.at(index), error)) {
            return false;
        }
        emit logMessage(QStringLiteral("文件发送完成：%1").arg(operations.at(index).path));
    }

    const quint64 completeRequestID = quint64(operations.size()) + 3;
    if (!writeAll(socket, SyncProtocol::buildFrame(SyncProtocol::MessageSyncComplete, completeRequestID, QByteArray()), kSyncMessageTimeoutMs, error)) {
        return false;
    }
    if (!expectAck(socket, completeRequestID, kSyncMessageTimeoutMs, &ignoredPayload, error)) {
        return false;
    }
    emit logMessage(QStringLiteral("本轮同步已完成。"));
    return true;
}

bool SourceConnector::decodeSnapshot(const QByteArray& json, QMap<QString, SnapshotEntry>* files, QString* error) const
{
    QJsonParseError parseError;
    const QJsonDocument document = QJsonDocument::fromJson(json, &parseError);
    if (parseError.error != QJsonParseError::NoError || !document.isObject()) {
        if (error != nullptr) {
            *error = QStringLiteral("快照 JSON 格式不正确。");
        }
        return false;
    }
    const QJsonObject root = document.object();
    const QJsonObject object = root.value(QStringLiteral("Files")).toObject();
    files->clear();
    for (auto it = object.begin(); it != object.end(); ++it) {
        const QJsonObject entryObject = it.value().toObject();
        SnapshotEntry entry;
        entry.path = entryObject.value(QStringLiteral("Path")).toString(it.key());
        entry.size = qint64(entryObject.value(QStringLiteral("Size")).toDouble(0));
        entry.hash = entryObject.value(QStringLiteral("Hash")).toString();
        if (entry.path.isEmpty() || entry.hash.isEmpty()) {
            if (error != nullptr) {
                *error = QStringLiteral("快照条目不完整。");
            }
            return false;
        }
        files->insert(entry.path, entry);
    }
    return true;
}

QList<SourceConnector::SnapshotEntry> SourceConnector::compareSnapshots(const QMap<QString, SnapshotEntry>& source, const QMap<QString, SnapshotEntry>& target) const
{
    QList<SnapshotEntry> operations;
    for (auto it = source.begin(); it != source.end(); ++it) {
        const SnapshotEntry targetEntry = target.value(it.key());
        if (!target.contains(it.key()) || targetEntry.hash != it.value().hash || targetEntry.size != it.value().size) {
            operations.append(it.value());
        }
    }
    return operations;
}

bool SourceConnector::sendFile(QSslSocket* socket, quint64 requestID, const SnapshotEntry& entry, QString* error)
{
    const QString absolutePath = QFileInfo(QDir(sourceFolder).filePath(QDir::fromNativeSeparators(entry.path))).absoluteFilePath();
    QFile file(absolutePath);
    if (!file.open(QIODevice::ReadOnly)) {
        if (error != nullptr) {
            *error = QStringLiteral("打开源文件失败：%1").arg(file.errorString());
        }
        return false;
    }
    const QByteArray hash = fileHash(absolutePath, error);
    if (hash.size() != kHashSize) {
        return false;
    }
    const QByteArray fileID = makeFileID(entry.path, hash);
    const QByteArray beginPayload = encodeFileBegin(entry.path, file.size(), hash, fileID);
    if (beginPayload.isEmpty()) {
        if (error != nullptr) {
            *error = QStringLiteral("文件开始消息生成失败。");
        }
        return false;
    }
    if (!writeAll(socket, SyncProtocol::buildFrame(SyncProtocol::MessageFileBegin, requestID, beginPayload), kSyncMessageTimeoutMs, error)) {
        return false;
    }
    QByteArray ackPayload;
    if (!expectAck(socket, requestID, kSyncMessageTimeoutMs, &ackPayload, error)) {
        return false;
    }
    qint64 offset = decodeOffset(ackPayload, error);
    if (offset < 0 || offset > file.size()) {
        return false;
    }
    if (!file.seek(offset)) {
        if (error != nullptr) {
            *error = QStringLiteral("定位源文件失败。");
        }
        return false;
    }

    while (offset < file.size()) {
        if (isCancelled(error)) {
            return false;
        }
        const qint64 remaining = file.size() - offset;
        const QByteArray chunk = file.read(qMin<qint64>(remaining, kMaxChunkSize));
        if (chunk.isEmpty() && file.error() != QFile::NoError) {
            if (error != nullptr) {
                *error = QStringLiteral("读取源文件失败：%1").arg(file.errorString());
            }
            return false;
        }
        if (chunk.isEmpty()) {
            break;
        }
        if (!writeAll(socket, SyncProtocol::buildFrame(SyncProtocol::MessageFileChunk, requestID, encodeFileChunk(offset, chunk)), kSyncMessageTimeoutMs, error)) {
            return false;
        }
        offset += chunk.size();
        if (!expectAck(socket, requestID, kSyncMessageTimeoutMs, &ackPayload, error)) {
            return false;
        }
        const qint64 confirmed = decodeOffset(ackPayload, error);
        if (confirmed != offset) {
            if (error != nullptr) {
                *error = QStringLiteral("目标端确认偏移不正确。");
            }
            return false;
        }
    }

    if (!writeAll(socket, SyncProtocol::buildFrame(SyncProtocol::MessageFileEnd, requestID, encodeFileEnd(file.size(), hash)), kSyncMessageTimeoutMs, error)) {
        return false;
    }
    return expectAck(socket, requestID, kSyncMessageTimeoutMs, &ackPayload, error);
}

bool SourceConnector::writeAll(QSslSocket* socket, const QByteArray& data, int timeoutMs, QString* error)
{
    qint64 offset = 0;
    QElapsedTimer timer;
    timer.start();
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
        offset += written;
        if (written > 0) {
            sentBytes += quint64(written);
            emitTrafficIfChanged();
        }
        const int remaining = timeoutMs - int(timer.elapsed());
        if (remaining <= 0) {
            if (error != nullptr) {
                *error = QStringLiteral("网络写入超时：%1").arg(socket->errorString());
            }
            return false;
        }
        if (!socket->waitForBytesWritten(qMin(remaining, 500)) && timer.elapsed() >= timeoutMs) {
            if (error != nullptr) {
                *error = QStringLiteral("网络写入超时：%1").arg(socket->errorString());
            }
            return false;
        }
    }
    return true;
}

QByteArray SourceConnector::readExact(QSslSocket* socket, int size, int timeoutMs, QString* error)
{
    QByteArray data;
    QElapsedTimer timer;
    timer.start();
    while (data.size() < size) {
        if (isCancelled(error)) {
            return {};
        }
        if (socket->bytesAvailable() <= 0) {
            const int remaining = timeoutMs - int(timer.elapsed());
            if (remaining <= 0) {
                if (error != nullptr) {
                    *error = QStringLiteral("网络读取超时或失败：%1").arg(socket->errorString());
                }
                return {};
            }
            if (!socket->waitForReadyRead(qMin(remaining, 500)) && timer.elapsed() >= timeoutMs) {
                if (error != nullptr) {
                    *error = QStringLiteral("网络读取超时或失败：%1").arg(socket->errorString());
                }
                return {};
            }
        }
        const QByteArray chunk = socket->read(size - data.size());
        if (!chunk.isEmpty()) {
            receivedBytes += quint64(chunk.size());
            emitTrafficIfChanged();
        }
        data.append(chunk);
    }
    return data;
}

bool SourceConnector::readFrame(QSslSocket* socket, int timeoutMs, SyncProtocol::Frame* frame, QString* error)
{
    const QByteArray header = readExact(socket, 14, timeoutMs, error);
    if (header.size() != 14) {
        return false;
    }
    const quint32 payloadLength = qFromBigEndian<quint32>(reinterpret_cast<const uchar*>(header.constData() + 10));
    if (payloadLength > kMaxPayload) {
        if (error != nullptr) {
            *error = QStringLiteral("目标端消息过大。");
        }
        return false;
    }
    const QByteArray payload = payloadLength == 0 ? QByteArray() : readExact(socket, int(payloadLength), timeoutMs, error);
    if (payload.size() != int(payloadLength)) {
        return false;
    }
    return SyncProtocol::parseFrame(header, payload, frame, error);
}

bool SourceConnector::expectAck(QSslSocket* socket, quint64 requestID, int timeoutMs, QByteArray* payload, QString* error)
{
    SyncProtocol::Frame frame;
    if (!readFrame(socket, timeoutMs, &frame, error)) {
        return false;
    }
    if (frame.requestID != requestID || frame.type != SyncProtocol::MessageAck) {
        if (error != nullptr) {
            *error = QStringLiteral("目标端没有确认同步消息。");
        }
        return false;
    }
    if (payload != nullptr) {
        *payload = frame.payload;
    }
    return true;
}

QByteArray SourceConnector::fileHash(const QString& absolutePath, QString* error) const
{
    QFile file(absolutePath);
    if (!file.open(QIODevice::ReadOnly)) {
        if (error != nullptr) {
            *error = QStringLiteral("读取源文件失败：%1").arg(file.errorString());
        }
        return {};
    }
    QCryptographicHash sha(QCryptographicHash::Sha256);
    while (!file.atEnd()) {
        const QByteArray chunk = file.read(kMaxChunkSize);
        if (chunk.isEmpty() && file.error() != QFile::NoError) {
            if (error != nullptr) {
                *error = QStringLiteral("读取源文件失败：%1").arg(file.errorString());
            }
            return {};
        }
        sha.addData(chunk);
    }
    return sha.result();
}

QByteArray SourceConnector::makeFileID(const QString& relativePath, const QByteArray& hash) const
{
    QCryptographicHash sha(QCryptographicHash::Sha256);
    sha.addData(relativePath.toUtf8());
    sha.addData(hash);
    return sha.result();
}

QByteArray SourceConnector::encodeFileBegin(const QString& relativePath, qint64 size, const QByteArray& hash, const QByteArray& fileID) const
{
    const QByteArray path = relativePath.toUtf8();
    if (path.isEmpty() || path.size() > 65535 || hash.size() != kHashSize || fileID.size() != kHashSize || size < 0) {
        return {};
    }
    QByteArray payload;
    payload.resize(2 + path.size() + 8 + kHashSize + kHashSize);
    qToBigEndian<quint16>(quint16(path.size()), reinterpret_cast<uchar*>(payload.data()));
    memcpy(payload.data() + 2, path.constData(), path.size());
    const int offset = 2 + path.size();
    qToBigEndian<quint64>(quint64(size), reinterpret_cast<uchar*>(payload.data() + offset));
    memcpy(payload.data() + offset + 8, hash.constData(), kHashSize);
    memcpy(payload.data() + offset + 8 + kHashSize, fileID.constData(), kHashSize);
    return payload;
}

QByteArray SourceConnector::encodeFileChunk(qint64 offset, const QByteArray& data) const
{
    QByteArray payload;
    payload.resize(8 + data.size());
    qToBigEndian<quint64>(quint64(offset), reinterpret_cast<uchar*>(payload.data()));
    memcpy(payload.data() + 8, data.constData(), data.size());
    return payload;
}

QByteArray SourceConnector::encodeFileEnd(qint64 size, const QByteArray& hash) const
{
    QByteArray payload;
    payload.resize(8 + kHashSize);
    qToBigEndian<quint64>(quint64(size), reinterpret_cast<uchar*>(payload.data()));
    memcpy(payload.data() + 8, hash.constData(), kHashSize);
    return payload;
}

qint64 SourceConnector::decodeOffset(const QByteArray& payload, QString* error) const
{
    if (payload.size() != 8) {
        if (error != nullptr) {
            *error = QStringLiteral("目标端确认偏移格式不正确。");
        }
        return -1;
    }
    const quint64 raw = qFromBigEndian<quint64>(reinterpret_cast<const uchar*>(payload.constData()));
    if (raw > quint64(std::numeric_limits<qint64>::max())) {
        if (error != nullptr) {
            *error = QStringLiteral("目标端确认偏移过大。");
        }
        return -1;
    }
    return qint64(raw);
}

void SourceConnector::emitTrafficIfChanged()
{
    if (receivedBytes == lastReportedReceivedBytes && sentBytes == lastReportedSentBytes) {
        return;
    }
    lastReportedReceivedBytes = receivedBytes;
    lastReportedSentBytes = sentBytes;
    emit trafficChanged(receivedBytes, sentBytes);
}
