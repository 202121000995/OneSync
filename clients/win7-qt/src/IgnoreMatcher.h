#pragma once

#include <QString>
#include <QStringList>

class IgnoreMatcher
{
public:
    explicit IgnoreMatcher(const QStringList& rules = QStringList());

    bool isEmpty() const;
    bool matches(const QString& relativePath, bool directory = false) const;
    QStringList normalizedRules() const;

private:
    struct Rule {
        QString pattern;
        bool directoryOnly = false;
        bool hasSlash = false;
        bool wildcard = false;
    };

    static QString normalizePath(const QString& path);
    static bool wildcardMatch(const QString& pattern, const QString& value);

    QList<Rule> rules;
};
