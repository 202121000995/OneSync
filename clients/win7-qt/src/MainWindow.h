#pragma once

#include "SyncLink.h"

#include <QMainWindow>

class QLabel;
class QLineEdit;
class QPushButton;
class QPlainTextEdit;
class QTextEdit;

class MainWindow : public QMainWindow
{
    Q_OBJECT

public:
    explicit MainWindow(QWidget* parent = nullptr);

private slots:
    void chooseTargetFolder();
    void parseLink();
    void startPlaceholder();
    void exportDiagnostics();

private:
    void appendLog(const QString& message);
    void updateLinkSummary(const SyncLink& link);
    QString diagnosticsText() const;

    QTextEdit* linkEdit = nullptr;
    QLineEdit* targetFolderEdit = nullptr;
    QLabel* sourceEndpointLabel = nullptr;
    QLabel* relayEndpointLabel = nullptr;
    QLabel* sessionLabel = nullptr;
    QLabel* expiresLabel = nullptr;
    QLabel* statusLabel = nullptr;
    QPlainTextEdit* logEdit = nullptr;
    QPushButton* startButton = nullptr;
    SyncLink currentLink;
    bool linkReady = false;
};
