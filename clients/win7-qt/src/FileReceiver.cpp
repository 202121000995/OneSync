#include "FileReceiver.h"

#include "IgnoreMatcher.h"

#include <QCryptographicHash>
#include <QDir>
#include <QElapsedTimer>
#include <QFile>
#include <QFileInfo>
#include <QSslSocket>
#include <QtEndian>

#include <limits>

namespace {
const int kSyncMessageTimeoutMs = 30000;
const int kMaxPayload = 16 * 1024 * 1024;
const int kMaxChunkSize = 256 * 1024;
const int kHashSize = 32;
const int kFileIDSize = 32;
const QString kPartDirectoryName = QStringLiteral(".onesync-part");
} // namespace

bool FileReceiver::receive(QSslSocket* socket, const QString& root, const SyncProtocol::Frame& beginFrame, Options* options, QString* transferredPath, QString* error)
{
    if (isCancelled(options, error)) {
        return false;
    }
    QString relativePath;
    qint64 totalSize = 0;
    QByteArray expectedHash;
    QByteArray fileID;
    if (beginFrame.type != SyncProtocol::MessageFileBegin ||
        !decodeBegin(beginFrame.payload, &relativePath, &totalSize, &expectedHash, &fileID, error)) {
        if (error != nullptr && error->isEmpty()) {
            *error = QStringLiteral("文件开始消息不正确。");
        }
        (void)writeError(socket, beginFrame.requestID, options);
        return false;
    }
    if (makeFileID(relativePath, expectedHash) != fileID) {
        if (error != nullptr) {
            *error = QStringLiteral("文件 ID 校验失败。");
        }
        (void)writeError(socket, beginFrame.requestID, options);
        return false;
    }

    IgnoreMatcher ignoreMatcher(options != nullptr ? options->ignoreRules : QStringList());
    if (ignoreMatcher.matches(relativePath, false)) {
        if (options != nullptr) {
            options->skipped = true;
        }
        if (transferredPath != nullptr) {
            *transferredPath = relativePath;
        }
        return discardFile(socket, beginFrame, totalSize, expectedHash, options, error);
    }
    if (options != nullptr) {
        options->skipped = false;
    }

    const QString targetPath = safeTargetPath(root, relativePath, error);
    if (targetPath.isEmpty() || !prepareTargetParent(root, relativePath, error)) {
        (void)writeError(socket, beginFrame.requestID, options);
        return false;
    }

    QString partDir;
    if (!preparePartDir(root, &partDir, error)) {
        (void)writeError(socket, beginFrame.requestID, options);
        return false;
    }
    const QString partPath = QDir(partDir).filePath(QString::fromLatin1(fileID.toHex()) + QStringLiteral(".part"));
    QFile part(partPath);
    if (!part.open(QIODevice::ReadWrite)) {
        if (error != nullptr) {
            *error = QStringLiteral("打开临时文件失败：%1").arg(part.errorString());
        }
        (void)writeError(socket, beginFrame.requestID, options);
        return false;
    }

    qint64 offset = part.size();
    if (offset > totalSize) {
        if (!part.resize(0)) {
            if (error != nullptr) {
                *error = QStringLiteral("重置临时文件失败：%1").arg(part.errorString());
            }
            (void)writeError(socket, beginFrame.requestID, options);
            return false;
        }
        offset = 0;
    }
    if (!part.seek(offset)) {
        if (error != nullptr) {
            *error = QStringLiteral("定位临时文件失败。");
        }
        (void)writeError(socket, beginFrame.requestID, options);
        return false;
    }

    if (!writeAck(socket, beginFrame.requestID, encodeOffset(offset), options, error)) {
        return false;
    }

    for (;;) {
        if (isCancelled(options, error)) {
            return false;
        }
        SyncProtocol::Frame frame;
        if (!readFrame(socket, kSyncMessageTimeoutMs, &frame, options, error)) {
            return false;
        }
        if (frame.requestID != beginFrame.requestID) {
            if (error != nullptr) {
                *error = QStringLiteral("文件传输 request id 不一致。");
            }
            (void)writeError(socket, frame.requestID, options);
            return false;
        }

        if (frame.type == SyncProtocol::MessageFileChunk) {
            qint64 chunkOffset = 0;
            QByteArray chunk;
            if (!decodeChunk(frame.payload, &chunkOffset, &chunk, error) || chunkOffset != offset || qint64(chunk.size()) > totalSize - offset) {
                if (error != nullptr && error->isEmpty()) {
                    *error = QStringLiteral("文件分块偏移或长度不正确。");
                }
                (void)writeError(socket, frame.requestID, options);
                return false;
            }
            if (part.write(chunk) != chunk.size()) {
                if (error != nullptr) {
                    *error = QStringLiteral("写入临时文件失败：%1").arg(part.errorString());
                }
                (void)writeError(socket, frame.requestID, options);
                return false;
            }
            if (!part.flush()) {
                if (error != nullptr) {
                    *error = QStringLiteral("刷新临时文件失败：%1").arg(part.errorString());
                }
                (void)writeError(socket, frame.requestID, options);
                return false;
            }
            offset += chunk.size();
            if (!writeAck(socket, frame.requestID, encodeOffset(offset), options, error)) {
                return false;
            }
            continue;
        }

        if (frame.type == SyncProtocol::MessageFileEnd) {
            qint64 endSize = 0;
            QByteArray endHash;
            if (!decodeEnd(frame.payload, &endSize, &endHash, error) || endSize != totalSize || endHash != expectedHash || offset != totalSize) {
                if (error != nullptr && error->isEmpty()) {
                    *error = QStringLiteral("文件结束元数据不匹配。");
                }
                (void)writeError(socket, frame.requestID, options);
                return false;
            }
            part.close();
            const QByteArray actualHash = hashFile(partPath, error);
            if (actualHash.isEmpty() || actualHash != expectedHash) {
                QFile::remove(partPath);
                if (error != nullptr && error->isEmpty()) {
                    *error = QStringLiteral("接收文件哈希校验失败。");
                }
                (void)writeError(socket, frame.requestID, options);
                return false;
            }
            QFile::remove(targetPath);
            if (!QFile::rename(partPath, targetPath)) {
                if (error != nullptr) {
                    *error = QStringLiteral("替换目标文件失败：%1").arg(relativePath);
                }
                (void)writeError(socket, frame.requestID, options);
                return false;
            }
            if (transferredPath != nullptr) {
                *transferredPath = relativePath;
            }
            return writeAck(socket, frame.requestID, QByteArray(), options, error);
        }

        if (error != nullptr) {
            *error = QStringLiteral("文件传输中收到未知消息。");
        }
        (void)writeError(socket, frame.requestID, options);
        return false;
    }
}

bool FileReceiver::decodeBegin(const QByteArray& payload, QString* path, qint64* size, QByteArray* hash, QByteArray* fileID, QString* error)
{
    if (payload.size() < 2) {
        if (error != nullptr) {
            *error = QStringLiteral("文件开始消息过短。");
        }
        return false;
    }
    const int pathLength = qFromBigEndian<quint16>(reinterpret_cast<const uchar*>(payload.constData()));
    const int expectedLength = 2 + pathLength + 8 + kHashSize + kFileIDSize;
    if (pathLength <= 0 || payload.size() != expectedLength) {
        if (error != nullptr) {
            *error = QStringLiteral("文件开始消息长度不正确。");
        }
        return false;
    }
    const int offset = 2 + pathLength;
    const QString decodedPath = QString::fromUtf8(payload.constData() + 2, pathLength);
    if (!validateRelativePath(decodedPath, error)) {
        return false;
    }
    const quint64 rawSize = qFromBigEndian<quint64>(reinterpret_cast<const uchar*>(payload.constData() + offset));
    if (rawSize > quint64(std::numeric_limits<qint64>::max())) {
        if (error != nullptr) {
            *error = QStringLiteral("文件大小超过支持范围。");
        }
        return false;
    }
    *path = decodedPath;
    *size = qint64(rawSize);
    *hash = payload.mid(offset + 8, kHashSize);
    *fileID = payload.mid(offset + 8 + kHashSize, kFileIDSize);
    return true;
}

bool FileReceiver::decodeChunk(const QByteArray& payload, qint64* offset, QByteArray* data, QString* error)
{
    if (payload.size() <= 8 || payload.size() - 8 > kMaxChunkSize) {
        if (error != nullptr) {
            *error = QStringLiteral("文件分块长度不正确。");
        }
        return false;
    }
    const quint64 rawOffset = qFromBigEndian<quint64>(reinterpret_cast<const uchar*>(payload.constData()));
    if (rawOffset > quint64(std::numeric_limits<qint64>::max())) {
        if (error != nullptr) {
            *error = QStringLiteral("文件分块偏移超过支持范围。");
        }
        return false;
    }
    *offset = qint64(rawOffset);
    *data = payload.mid(8);
    return true;
}

bool FileReceiver::decodeEnd(const QByteArray& payload, qint64* size, QByteArray* hash, QString* error)
{
    if (payload.size() != 8 + kHashSize) {
        if (error != nullptr) {
            *error = QStringLiteral("文件结束消息长度不正确。");
        }
        return false;
    }
    const quint64 rawSize = qFromBigEndian<quint64>(reinterpret_cast<const uchar*>(payload.constData()));
    if (rawSize > quint64(std::numeric_limits<qint64>::max())) {
        if (error != nullptr) {
            *error = QStringLiteral("文件大小超过支持范围。");
        }
        return false;
    }
    *size = qint64(rawSize);
    *hash = payload.mid(8, kHashSize);
    return true;
}

bool FileReceiver::validateRelativePath(const QString& path, QString* error)
{
    const QString normalized = QDir::fromNativeSeparators(path);
    if (normalized.isEmpty() || normalized.startsWith(QLatin1Char('/')) ||
        normalized.contains(QLatin1Char('\\')) || normalized.contains(QLatin1Char(':')) ||
        normalized.contains(QChar(0)) || normalized == QStringLiteral(".") || normalized == QStringLiteral("..") ||
        normalized.startsWith(QLatin1String("../")) || QDir::cleanPath(normalized) != normalized) {
        if (error != nullptr) {
            *error = QStringLiteral("文件相对路径不安全：%1").arg(path);
        }
        return false;
    }
    return true;
}

QString FileReceiver::safeTargetPath(const QString& root, const QString& relativePath, QString* error)
{
    if (!validateRelativePath(relativePath, error)) {
        return {};
    }
    const QDir rootDir(QFileInfo(root).absoluteFilePath());
    const QString target = QFileInfo(rootDir.filePath(QDir::fromNativeSeparators(relativePath))).absoluteFilePath();
    const QString relative = QDir::fromNativeSeparators(rootDir.relativeFilePath(target));
    if (relative == QStringLiteral("..") || relative.startsWith(QLatin1String("../"))) {
        if (error != nullptr) {
            *error = QStringLiteral("目标路径越过同步目录。");
        }
        return {};
    }
    return target;
}

bool FileReceiver::prepareTargetParent(const QString& root, const QString& relativePath, QString* error)
{
    const QString directory = QFileInfo(QDir::fromNativeSeparators(relativePath)).path();
    if (directory == QStringLiteral(".")) {
        return true;
    }
    QDir rootDir(root);
    const QStringList parts = directory.split(QLatin1Char('/'), QString::SkipEmptyParts);
    QString current = rootDir.absolutePath();
    for (const QString& part : parts) {
        current = QDir(current).filePath(part);
        QFileInfo info(current);
        if (!info.exists()) {
            if (!QDir().mkdir(current)) {
                if (error != nullptr) {
                    *error = QStringLiteral("创建目标目录失败：%1").arg(current);
                }
                return false;
            }
            info = QFileInfo(current);
        }
        if (!info.isDir() || info.isSymLink()) {
            if (error != nullptr) {
                *error = QStringLiteral("目标父目录不是普通目录：%1").arg(current);
            }
            return false;
        }
    }
    return true;
}

bool FileReceiver::preparePartDir(const QString& root, QString* partDir, QString* error)
{
    const QString path = QDir(root).filePath(kPartDirectoryName);
    QFileInfo info(path);
    if (!info.exists()) {
        if (!QDir().mkdir(path)) {
            if (error != nullptr) {
                *error = QStringLiteral("创建临时同步目录失败。");
            }
            return false;
        }
        info = QFileInfo(path);
    }
    if (!info.isDir() || info.isSymLink()) {
        if (error != nullptr) {
            *error = QStringLiteral("临时同步路径不是普通目录。");
        }
        return false;
    }
    *partDir = info.absoluteFilePath();
    return true;
}

QByteArray FileReceiver::makeFileID(const QString& path, const QByteArray& hash)
{
    QCryptographicHash sha(QCryptographicHash::Sha256);
    sha.addData(path.toUtf8());
    sha.addData(hash);
    return sha.result();
}

QByteArray FileReceiver::encodeOffset(qint64 offset)
{
    QByteArray payload(8, Qt::Uninitialized);
    qToBigEndian<quint64>(quint64(offset), reinterpret_cast<uchar*>(payload.data()));
    return payload;
}

QByteArray FileReceiver::hashFile(const QString& path, QString* error)
{
    QFile file(path);
    if (!file.open(QIODevice::ReadOnly)) {
        if (error != nullptr) {
            *error = QStringLiteral("读取临时文件失败：%1").arg(file.errorString());
        }
        return {};
    }
    QCryptographicHash sha(QCryptographicHash::Sha256);
    while (!file.atEnd()) {
        const QByteArray chunk = file.read(kMaxChunkSize);
        if (chunk.isEmpty() && file.error() != QFile::NoError) {
            if (error != nullptr) {
                *error = QStringLiteral("读取临时文件失败：%1").arg(file.errorString());
            }
            return {};
        }
        sha.addData(chunk);
    }
    return sha.result();
}

bool FileReceiver::isCancelled(const Options* options, QString* error)
{
    if (options != nullptr && options->cancelled != nullptr && options->cancelled->loadAcquire() != 0) {
        if (error != nullptr) {
            *error = QStringLiteral("同步已取消。");
        }
        return true;
    }
    return false;
}

bool FileReceiver::discardFile(QSslSocket* socket, const SyncProtocol::Frame& beginFrame, qint64 totalSize, const QByteArray& expectedHash, Options* options, QString* error)
{
    qint64 offset = 0;
    QCryptographicHash sha(QCryptographicHash::Sha256);
    if (!writeAck(socket, beginFrame.requestID, encodeOffset(offset), options, error)) {
        return false;
    }

    for (;;) {
        if (isCancelled(options, error)) {
            return false;
        }
        SyncProtocol::Frame frame;
        if (!readFrame(socket, kSyncMessageTimeoutMs, &frame, options, error)) {
            return false;
        }
        if (frame.requestID != beginFrame.requestID) {
            if (error != nullptr) {
                *error = QStringLiteral("忽略文件传输 request id 不一致。");
            }
            (void)writeError(socket, frame.requestID, options);
            return false;
        }
        if (frame.type == SyncProtocol::MessageFileChunk) {
            qint64 chunkOffset = 0;
            QByteArray chunk;
            if (!decodeChunk(frame.payload, &chunkOffset, &chunk, error) || chunkOffset != offset || qint64(chunk.size()) > totalSize - offset) {
                if (error != nullptr && error->isEmpty()) {
                    *error = QStringLiteral("忽略文件分块偏移或长度不正确。");
                }
                (void)writeError(socket, frame.requestID, options);
                return false;
            }
            sha.addData(chunk);
            offset += chunk.size();
            if (!writeAck(socket, frame.requestID, encodeOffset(offset), options, error)) {
                return false;
            }
            continue;
        }
        if (frame.type == SyncProtocol::MessageFileEnd) {
            qint64 endSize = 0;
            QByteArray endHash;
            if (!decodeEnd(frame.payload, &endSize, &endHash, error) || endSize != totalSize || offset != totalSize || endHash != expectedHash || sha.result() != expectedHash) {
                if (error != nullptr && error->isEmpty()) {
                    *error = QStringLiteral("忽略文件结束元数据不匹配。");
                }
                (void)writeError(socket, frame.requestID, options);
                return false;
            }
            return writeAck(socket, frame.requestID, QByteArray(), options, error);
        }
        if (error != nullptr) {
            *error = QStringLiteral("忽略文件传输中收到未知消息。");
        }
        (void)writeError(socket, frame.requestID, options);
        return false;
    }
}

bool FileReceiver::writeAck(QSslSocket* socket, quint64 requestID, const QByteArray& payload, Options* options, QString* error)
{
    return writeAll(socket, SyncProtocol::buildFrame(SyncProtocol::MessageAck, requestID, payload), kSyncMessageTimeoutMs, options, error);
}

bool FileReceiver::writeError(QSslSocket* socket, quint64 requestID, Options* options)
{
    QString ignored;
    return writeAll(socket, SyncProtocol::buildFrame(SyncProtocol::MessageError, requestID, QByteArray()), kSyncMessageTimeoutMs, options, &ignored);
}

bool FileReceiver::writeAll(QSslSocket* socket, const QByteArray& data, int timeoutMs, Options* options, QString* error)
{
    qint64 offset = 0;
    QElapsedTimer timer;
    timer.start();
    while (offset < data.size()) {
        if (isCancelled(options, error)) {
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
        if (options != nullptr && options->sentBytes != nullptr && written > 0) {
            *options->sentBytes += quint64(written);
            if (options->onTrafficChanged) {
                options->onTrafficChanged();
            }
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

QByteArray FileReceiver::readExact(QSslSocket* socket, int size, int timeoutMs, Options* options, QString* error)
{
    QByteArray data;
    QElapsedTimer timer;
    timer.start();
    while (data.size() < size) {
        if (isCancelled(options, error)) {
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
        if (options != nullptr && options->receivedBytes != nullptr && !chunk.isEmpty()) {
            *options->receivedBytes += quint64(chunk.size());
            if (options->onTrafficChanged) {
                options->onTrafficChanged();
            }
        }
        data.append(chunk);
    }
    return data;
}

bool FileReceiver::readFrame(QSslSocket* socket, int timeoutMs, SyncProtocol::Frame* frame, Options* options, QString* error)
{
    const QByteArray header = readExact(socket, 14, timeoutMs, options, error);
    if (header.size() != 14) {
        return false;
    }
    const quint32 payloadLength = qFromBigEndian<quint32>(reinterpret_cast<const uchar*>(header.constData() + 10));
    if (payloadLength > kMaxPayload) {
        if (error != nullptr) {
            *error = QStringLiteral("源端消息过大。");
        }
        return false;
    }
    const QByteArray payload = payloadLength == 0 ? QByteArray() : readExact(socket, int(payloadLength), timeoutMs, options, error);
    if (payload.size() != int(payloadLength)) {
        return false;
    }
    return SyncProtocol::parseFrame(header, payload, frame, error);
}
