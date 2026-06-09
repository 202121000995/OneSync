#include "PeerIdentity.h"

#include <QCryptographicHash>
#include <QRandomGenerator>
#include <QSettings>

namespace {
QString settingsKey(const QString& sessionID)
{
    const QByteArray hash = QCryptographicHash::hash(sessionID.toUtf8(), QCryptographicHash::Sha256).toHex();
    return QStringLiteral("peers/%1/peer_id").arg(QString::fromLatin1(hash));
}
} // namespace

QString PeerIdentityStore::peerIDForSession(const QString& sessionID)
{
    QSettings settings(QStringLiteral("OneSync"), QStringLiteral("OneSyncWin7"));
    const QString key = settingsKey(sessionID);
    const QString existing = settings.value(key).toString();
    if (!existing.isEmpty()) {
        return existing;
    }
    const QString created = newPeerID();
    settings.setValue(key, created);
    settings.sync();
    return created;
}

QString PeerIdentityStore::newPeerID()
{
    QByteArray bytes(32, Qt::Uninitialized);
    for (int i = 0; i < bytes.size(); ++i) {
        bytes[i] = char(QRandomGenerator::system()->bounded(256));
    }
    return QString::fromLatin1(bytes.toBase64(QByteArray::Base64UrlEncoding | QByteArray::OmitTrailingEquals));
}
