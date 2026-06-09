#pragma once

#include "SyncLink.h"

#include <QMainWindow>

class QAction;
class QCloseEvent;
class QLabel;
class QLineEdit;
class QMenu;
class QPushButton;
class QPlainTextEdit;
class QSystemTrayIcon;
class QThread;
class QTextEdit;

class MainWindow : public QMainWindow
{
    Q_OBJECT

public:
    explicit MainWindow(QWidget* parent = nullptr);

private slots:
    void chooseTargetFolder();
    void parseLink();
    void startSync();
    void exportDiagnostics();
    void showFromTray();
    void quitFromTray();

private:
    void closeEvent(QCloseEvent* event) override;
    void setupTray();
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
    QThread* connectionThread = nullptr;
    QSystemTrayIcon* trayIcon = nullptr;
    QMenu* trayMenu = nullptr;
    bool exiting = false;
    bool trayCloseTipShown = false;
    SyncLink currentLink;
    bool linkReady = false;
};
