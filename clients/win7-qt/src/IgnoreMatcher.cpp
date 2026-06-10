#include "IgnoreMatcher.h"

#include <QDir>
#include <QFileInfo>
#include <QRegExp>

IgnoreMatcher::IgnoreMatcher(const QStringList& inputRules)
{
    for (QString ruleText : inputRules) {
        ruleText = ruleText.trimmed();
        if (ruleText.isEmpty() || ruleText.startsWith(QLatin1Char('#'))) {
            continue;
        }
        ruleText = normalizePath(ruleText);
        while (ruleText.startsWith(QLatin1String("./"))) {
            ruleText.remove(0, 2);
        }
        if (ruleText.isEmpty()) {
            continue;
        }

        Rule rule;
        rule.directoryOnly = ruleText.endsWith(QLatin1Char('/'));
        if (rule.directoryOnly) {
            while (ruleText.endsWith(QLatin1Char('/'))) {
                ruleText.chop(1);
            }
        }
        if (ruleText.isEmpty()) {
            continue;
        }
        rule.pattern = ruleText;
        rule.hasSlash = rule.pattern.contains(QLatin1Char('/'));
        rule.wildcard = rule.pattern.contains(QLatin1Char('*')) || rule.pattern.contains(QLatin1Char('?'));
        rules.append(rule);
    }
}

bool IgnoreMatcher::isEmpty() const
{
    return rules.isEmpty();
}

bool IgnoreMatcher::matches(const QString& relativePath, bool directory) const
{
    const QString path = normalizePath(relativePath);
    const QString name = QFileInfo(path).fileName();
    for (const Rule& rule : rules) {
        if (rule.directoryOnly && !directory && !path.startsWith(rule.pattern + QLatin1Char('/'))) {
            continue;
        }

        if (rule.hasSlash) {
            if (rule.wildcard) {
                if (wildcardMatch(rule.pattern, path) || wildcardMatch(rule.pattern + QStringLiteral("/*"), path)) {
                    return true;
                }
                continue;
            }
            if (path == rule.pattern || path.startsWith(rule.pattern + QLatin1Char('/'))) {
                return true;
            }
            continue;
        }

        if (rule.wildcard) {
            if (wildcardMatch(rule.pattern, name)) {
                return true;
            }
            continue;
        }

        if (name == rule.pattern || path == rule.pattern || path.startsWith(rule.pattern + QLatin1Char('/'))) {
            return true;
        }
    }
    return false;
}

QStringList IgnoreMatcher::normalizedRules() const
{
    QStringList output;
    for (const Rule& rule : rules) {
        output.append(rule.directoryOnly ? rule.pattern + QLatin1Char('/') : rule.pattern);
    }
    return output;
}

QString IgnoreMatcher::normalizePath(const QString& path)
{
    QString normalized = QDir::fromNativeSeparators(path.trimmed());
    while (normalized.contains(QLatin1String("//"))) {
        normalized.replace(QStringLiteral("//"), QStringLiteral("/"));
    }
    return normalized;
}

bool IgnoreMatcher::wildcardMatch(const QString& pattern, const QString& value)
{
    QRegExp expression(pattern, Qt::CaseInsensitive, QRegExp::Wildcard);
    return expression.exactMatch(value);
}
