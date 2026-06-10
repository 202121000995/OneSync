#pragma once

#include <QByteArray>
#include <QString>
#include <QStringList>

class SnapshotScanner
{
public:
    static bool scanToJson(const QString& root, QByteArray* json, quint64* fileCount, quint64* byteCount, QString* error);
    static bool scanToJson(const QString& root, const QStringList& ignoreRules, QByteArray* json, quint64* fileCount, quint64* byteCount, quint64* ignoredCount, QString* error);
};
