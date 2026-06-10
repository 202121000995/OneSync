#include "SnapshotScanner.h"

#include "IgnoreMatcher.h"

#include <QCryptographicHash>
#include <QDateTime>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QJsonArray>
#include <QJsonDocument>
#include <QList>

#include <algorithm>

namespace {
const QString kReservedTransferDirectory = QStringLiteral(".onesync-part");

struct SnapshotEntry
{
    QString path;
    qint64 size = 0;
    qint64 modTime = 0;
    quint32 mode = 0;
    QString hash;
};

QString rootID(const QString& absoluteRoot)
{
    const QByteArray digest = QCryptographicHash::hash(QDir::cleanPath(absoluteRoot).toUtf8(), QCryptographicHash::Sha256);
    return QString::fromLatin1(digest.toHex());
}

QString toSlashPath(const QString& path)
{
    QString normalized = QDir::fromNativeSeparators(path);
    while (normalized.startsWith(QLatin1String("./"))) {
        normalized.remove(0, 2);
    }
    return normalized;
}

bool hashFile(const QString& path, QString* hash, QString* error)
{
    QFile file(path);
    if (!file.open(QIODevice::ReadOnly)) {
        if (error != nullptr) {
            *error = QStringLiteral("读取文件失败：%1：%2").arg(path, file.errorString());
        }
        return false;
    }

    QCryptographicHash md5(QCryptographicHash::Md5);
    while (!file.atEnd()) {
        const QByteArray chunk = file.read(1024 * 1024);
        if (chunk.isEmpty() && file.error() != QFile::NoError) {
            if (error != nullptr) {
                *error = QStringLiteral("读取文件失败：%1：%2").arg(path, file.errorString());
            }
            return false;
        }
        md5.addData(chunk);
    }
    *hash = QString::fromLatin1(md5.result().toHex());
    return true;
}

QByteArray jsonString(const QString& value)
{
    QJsonArray array;
    array.append(value);
    QByteArray encoded = QJsonDocument(array).toJson(QJsonDocument::Compact);
    if (encoded.startsWith('[')) {
        encoded.remove(0, 1);
    }
    if (encoded.endsWith(']')) {
        encoded.chop(1);
    }
    return encoded;
}

QByteArray number(qint64 value)
{
    return QByteArray::number(value);
}

bool scanDirectory(
    const QDir& rootDir,
    const QString& absolutePath,
    const IgnoreMatcher& ignoreMatcher,
    QList<SnapshotEntry>* entries,
    quint64* fileCount,
    quint64* byteCount,
    quint64* ignoredCount,
    QString* error
)
{
    const QFileInfoList directoryEntries = QDir(absolutePath).entryInfoList(
        QDir::Files | QDir::Dirs | QDir::NoDotAndDotDot,
        QDir::Name | QDir::DirsFirst
    );
    for (const QFileInfo& info : directoryEntries) {
        if (info.isSymLink()) {
            continue;
        }
        QString relativePath = toSlashPath(rootDir.relativeFilePath(info.absoluteFilePath()));
        if (relativePath.isEmpty() || relativePath == QStringLiteral(".") || relativePath.startsWith(QLatin1String("../"))) {
            if (error != nullptr) {
                *error = QStringLiteral("扫描到不安全路径：%1").arg(relativePath);
            }
            return false;
        }

        if (ignoreMatcher.matches(relativePath, info.isDir())) {
            ++(*ignoredCount);
            continue;
        }

        if (info.isDir()) {
            if (info.fileName() == kReservedTransferDirectory) {
                continue;
            }
            if (!scanDirectory(rootDir, info.absoluteFilePath(), ignoreMatcher, entries, fileCount, byteCount, ignoredCount, error)) {
                return false;
            }
            continue;
        }
        if (!info.isFile()) {
            continue;
        }

        QString hash;
        if (!hashFile(info.absoluteFilePath(), &hash, error)) {
            return false;
        }

        SnapshotEntry entry;
        entry.path = relativePath;
        entry.size = info.size();
        entry.modTime = info.lastModified().toUTC().toMSecsSinceEpoch() * 1000000;
        entry.mode = 0;
        entry.hash = hash;
        entries->append(entry);
        ++(*fileCount);
        if (info.size() > 0) {
            *byteCount += quint64(info.size());
        }
    }
    return true;
}

QByteArray snapshotJson(const QString& root, QList<SnapshotEntry> entries)
{
    std::sort(entries.begin(), entries.end(), [](const SnapshotEntry& left, const SnapshotEntry& right) {
        return left.path < right.path;
    });

    QByteArray json;
    json += "{\"RootID\":";
    json += jsonString(rootID(root));
    json += ",\"GeneratedAt\":";
    json += number(QDateTime::currentDateTimeUtc().toMSecsSinceEpoch() * 1000000);
    json += ",\"Files\":{";
    for (int i = 0; i < entries.size(); ++i) {
        const SnapshotEntry& entry = entries.at(i);
        if (i > 0) {
            json += ",";
        }
        json += jsonString(entry.path);
        json += ":{\"Path\":";
        json += jsonString(entry.path);
        json += ",\"Size\":";
        json += number(entry.size);
        json += ",\"ModTime\":";
        json += number(entry.modTime);
        json += ",\"Mode\":";
        json += QByteArray::number(entry.mode);
        json += ",\"Hash\":";
        json += jsonString(entry.hash);
        json += "}";
    }
    json += "}}";
    return json;
}
} // namespace

bool SnapshotScanner::scanToJson(const QString& root, QByteArray* json, quint64* fileCount, quint64* byteCount, QString* error)
{
    quint64 ignoredCount = 0;
    return scanToJson(root, QStringList(), json, fileCount, byteCount, &ignoredCount, error);
}

bool SnapshotScanner::scanToJson(const QString& root, const QStringList& ignoreRules, QByteArray* json, quint64* fileCount, quint64* byteCount, quint64* ignoredCount, QString* error)
{
    if (json == nullptr || fileCount == nullptr || byteCount == nullptr) {
        if (error != nullptr) {
            *error = QStringLiteral("内部错误：快照输出为空。");
        }
        return false;
    }
    quint64 localIgnoredCount = 0;
    if (ignoredCount == nullptr) {
        ignoredCount = &localIgnoredCount;
    }

    const QFileInfo rootInfo(root);
    if (!rootInfo.exists() || !rootInfo.isDir()) {
        if (error != nullptr) {
            *error = QStringLiteral("接收文件夹不存在或不是目录。");
        }
        return false;
    }

    *fileCount = 0;
    *byteCount = 0;
    *ignoredCount = 0;
    QDir rootDir(rootInfo.absoluteFilePath());
    QList<SnapshotEntry> entries;
    const IgnoreMatcher ignoreMatcher(ignoreRules);
    if (!scanDirectory(rootDir, rootInfo.absoluteFilePath(), ignoreMatcher, &entries, fileCount, byteCount, ignoredCount, error)) {
        return false;
    }

    *json = snapshotJson(rootInfo.absoluteFilePath(), entries);
    return true;
}
