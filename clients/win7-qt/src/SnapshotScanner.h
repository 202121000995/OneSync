#pragma once

#include <QByteArray>
#include <QString>

class SnapshotScanner
{
public:
    static bool scanToJson(const QString& root, QByteArray* json, quint64* fileCount, quint64* byteCount, QString* error);
};
