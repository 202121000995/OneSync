#pragma once

#include <QString>

class PeerIdentityStore
{
public:
    static QString peerIDForSession(const QString& sessionID);

private:
    static QString newPeerID();
};
