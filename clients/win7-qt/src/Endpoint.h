#pragma once

#include <QString>

struct Endpoint
{
    QString host;
    quint16 port = 0;

    bool isValid() const;
    QString display() const;
};

class EndpointParser
{
public:
    static bool parse(const QString& value, Endpoint* endpoint, QString* error);
};
